package queue

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/tomapedidos/print-agent/internal/config"
	"github.com/tomapedidos/print-agent/internal/printer"
)

// Worker drains jobs for a single printer. One Worker is spawned per
// registered Printer. The worker loop blocks on Queue.Notify() so it
// can stay idle without busy-waiting.
type Worker struct {
	printerID string
	queue     *Queue
	registry  *printer.Registry
	cfg       config.Queue
	log       *slog.Logger
}

// NewWorker returns a Worker that drives jobs for printerID.
func NewWorker(printerID string, q *Queue, reg *printer.Registry, cfg config.Queue, log *slog.Logger) *Worker {
	return &Worker{
		printerID: printerID,
		queue:     q,
		registry:  reg,
		cfg:       cfg,
		log:       log,
	}
}

// Run blocks until ctx is cancelled. It is safe to call exactly once.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.queue.Notify():
		case <-ticker.C:
		}
		w.drain(ctx)
	}
}

func (w *Worker) drain(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		job := w.queue.Pop(w.printerID)
		if job == nil {
			return
		}
		w.processOne(ctx, job)
	}
}

func (w *Worker) processOne(ctx context.Context, job *Job) {
	pr, ok := w.registry.Printers()[w.printerID]
	if !ok {
		w.queue.MarkFailed(job, "printer not registered")
		return
	}
	if err := pr.Write(ctx, job.Payload); err != nil {
		w.log.Warn("print failed",
			slog.String("job_id", job.ID),
			slog.String("printer", w.printerID),
			slog.Int("attempt", job.Attempts),
			slog.String("error", err.Error()),
		)
		w.registry.SetStatus(w.printerID, printer.StatusError, err.Error())
		w.queue.MarkFailed(job, err.Error())
		w.sleepBackoff(ctx, job.Attempts)
		// Wake the loop so a later retry can run quickly.
		w.queue.Wake()
		return
	}
	w.registry.MarkPrinted(w.printerID)
	w.queue.MarkPrinted(job)
	w.log.Info("print ok",
		slog.String("job_id", job.ID),
		slog.String("printer", w.printerID),
		slog.Int("bytes", job.Bytes),
	)
}

// sleepBackoff blocks for the configured retry delay for attempt n. It
// returns early if ctx is cancelled. The default schedule is taken from
// the JSON config (queue.retry_backoff_ms); a missing/short slice falls
// back to 1s.
func (w *Worker) sleepBackoff(ctx context.Context, attempt int) {
	if len(w.cfg.RetryBackoffMs) == 0 {
		return
	}
	idx := attempt - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(w.cfg.RetryBackoffMs) {
		idx = len(w.cfg.RetryBackoffMs) - 1
	}
	d := time.Duration(w.cfg.RetryBackoffMs[idx]) * time.Millisecond
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// Pool spawns one Worker per registered printer and runs them until ctx
// is cancelled.
type Pool struct {
	queue    *Queue
	registry *printer.Registry
	cfg      config.Queue
	log      *slog.Logger
}

// NewPool returns a worker pool.
func NewPool(q *Queue, reg *printer.Registry, cfg config.Queue, log *slog.Logger) *Pool {
	return &Pool{queue: q, registry: reg, cfg: cfg, log: log}
}

// Run starts one Worker per registered printer. It returns when ctx is
// cancelled. New printers added at runtime are NOT picked up by this
// call; that is done by the supervisor loop in main which calls Add().
func (p *Pool) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for id := range p.registry.Printers() {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			NewWorker(id, p.queue, p.registry, p.cfg, p.log).Run(ctx)
		}(id)
	}
	wg.Wait()
}

// Add spawns a new Worker for printerID. The caller must have already
// registered the printer with the registry. Intended for hot-reload
// scenarios (e.g. config reload adding a new printer).
func (p *Pool) Add(ctx context.Context, printerID string) {
	go NewWorker(printerID, p.queue, p.registry, p.cfg, p.log).Run(ctx)
}
