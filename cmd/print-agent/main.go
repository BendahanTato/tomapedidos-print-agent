// Command print-agent is the local print service for tomaPedidos. It binds
// to 127.0.0.1 only and exposes a small JSON API that the cashier browser
// hits to print comandas silently, without window.print() or any cloud
// relay.
//
// Subcommands:
//
//	print-agent start                      run in foreground (default if none given)
//	print-agent version                    print build version
//	print-agent init-config [path]         write a starter config to path (default ./configs/printers.json)
//	print-agent doctor                     print environment diagnostics
//
// M1 supports the start subcommand plus network (TCP 9100) and file printers.
// M2 will add USB via OS spooler. M3 will replace the in-memory queue with
// SQLite. M5 will ship the embedded web panel. M7 will add real service
// mode (launchd / systemd / SCM).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/tomapedidos/print-agent/internal/config"
	"github.com/tomapedidos/print-agent/internal/logging"
	"github.com/tomapedidos/print-agent/internal/printer"
	"github.com/tomapedidos/print-agent/internal/queue"
	"github.com/tomapedidos/print-agent/internal/server"
	"github.com/tomapedidos/print-agent/internal/version"
)

const (
	defaultConfigPath = "./configs/printers.json"
	defaultLogLevel   = "info"
)

func main() {
	if len(os.Args) < 2 {
		runStart(os.Args[1:], os.Stdout, os.Stderr)
		return
	}
	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Println(version.String())
	case "init-config":
		runInitConfig(os.Args[2:])
	case "doctor":
		runDoctor(os.Args[2:])
	case "start":
		runStart(os.Args[2:], os.Stdout, os.Stderr)
	case "help", "--help", "-h":
		printHelp()
	default:
		// Allow `./print-agent` with no subcommand (legacy).
		if strings.HasPrefix(os.Args[1], "-") {
			runStart(os.Args[1:], os.Stdout, os.Stderr)
			return
		}
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", os.Args[1])
		printHelp()
		os.Exit(2)
	}
}

func printHelp() {
	fmt.Print(`print-agent — TomaPedidos local print service

Subcommands:
  start [flags]           Run the agent in the foreground (default).
  version                Print version and exit.
  init-config [path]     Write a starter config to path.
  doctor                 Print environment diagnostics.
  help                   Print this help.

Flags for start:
  --config <path>        Path to config JSON (default ./configs/printers.json)
  --port <n>             Override listen port
  --bind <ip>            Override bind address (default 127.0.0.1)
  --log-level <name>     debug | info | warn | error (default info)
`)
}

func runInitConfig(args []string) {
	fs := flag.NewFlagSet("init-config", flag.ExitOnError)
	path := fs.String("config", defaultConfigPath, "path to write the starter config")
	_ = fs.Parse(args)
	cfg := config.Default()
	if err := os.MkdirAll(filepath.Dir(*path), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create dir: %v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stat(*path); err == nil {
		fmt.Fprintf(os.Stderr, "refusing to overwrite existing %s\n", *path)
		os.Exit(1)
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*path, b, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("wrote starter config to %s\n", *path)
}

func runDoctor(_ []string) {
	fmt.Printf("print-agent %s\n", version.String())
	fmt.Printf("go:       %s\n", runtime.Version())
	fmt.Printf("platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	if hasLoopback() {
		fmt.Println("loopback: ok")
	} else {
		fmt.Println("loopback: FAILED to bind 127.0.0.1 (very unusual)")
	}
}

func hasLoopback() bool {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

func runStart(args []string, stdout, stderr *os.File) {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath, "path to config JSON")
	port := fs.Int("port", 0, "override listen port")
	bind := fs.String("bind", "", "override bind address")
	logLevel := fs.String("log-level", defaultLogLevel, "log level (debug|info|warn|error)")
	_ = fs.Parse(args)

	log := logging.New(logging.Level(*logLevel), "print-agent", version.Version)

	cfgStore, err := config.Load(*configPath, true)
	if err != nil {
		log.Error("config load failed", slog.String("error", err.Error()))
		fmt.Fprintf(stderr, "config load failed: %v\n", err)
		os.Exit(1)
	}
	log.Info("config loaded", slog.String("path", cfgStore.Path()))

	cfg := cfgStore.Get()
	listenHost := cfg.Bind
	if *bind != "" {
		listenHost = *bind
	}
	listenPort := cfg.Port
	if *port > 0 {
		listenPort = *port
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	reg := printer.NewRegistry()
	for _, p := range cfg.Printers {
		pr, info, perr := printer.NewFromConfig(ctx, p)
		if perr != nil {
			log.Warn("printer init failed",
				slog.String("printer", p.ID),
				slog.String("error", perr.Error()),
			)
		}
		if pr != nil {
			reg.Add(pr, info)
		}
		// If pr is nil the printer is shown as offline by NewFromConfig
		// already (network printer that refused to connect). We still
		// want it visible in /printers so the operator knows it exists.
		if pr == nil {
			// Try a minimal stub so the registry has something to call
			// (the heartbeat will keep the status as offline).
			//
			// In M2/M3 we will handle USB cold-start with proper
			// reconnect logic. For M1 we accept the printer as visible
			// but unable to write.
			log.Info("printer registered as offline",
				slog.String("printer", p.ID),
				slog.String("reason", perr.Error()),
			)
		}
	}

	q, err := queue.New(cfg.Queue.MaxRetries, cfg.Queue.DedupWindow, cfg.Queue.PersistPath, log)
	if err != nil {
		log.Error("queue init failed", slog.String("error", err.Error()))
		fmt.Fprintf(stderr, "queue init failed: %v\n", err)
		os.Exit(1)
	}
	for id := range reg.Printers() {
		reg.SetQueueDepth(id, 0)
	}

	// Worker pool drains the queue.
	go queue.NewPool(q, reg, cfg.Queue, log).Run(ctx)

	// Heartbeat keeps Status fresh.
	go printer.Heartbeat(ctx, reg, log, 10*time.Second)

	mux := server.New(server.Deps{
		Config:    cfgStore,
		Registry:  reg,
		Queue:     q,
		Log:       log,
		StartedAt: time.Now(),
	})

	addr := fmt.Sprintf("%s:%d", listenHost, listenPort)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Error("listen failed", slog.String("addr", addr), slog.String("error", err.Error()))
		fmt.Fprintf(stderr, "listen %s: %v\n", addr, err)
		os.Exit(1)
	}
	log.Info("listening", slog.String("addr", addr))

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server crashed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	log.Info("bye")
}
