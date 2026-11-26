package queue

import (
	"context"
	"errors"
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
		if errors.Is(err, printer.ErrAmbiguousWrite) {
			w.registry.SetStatus(w.printerID, printer.StatusError, err.Error())
			w.log.Error("ambiguous print write; job will not be retried; the print may have partially succeeded, operator should verify",
				slog.String("job_id", job.ID),
				slog.String("printer", w.printerID),
				slog.String("error", err.Error()),
			)
			job.MaxAttempts = job.Attempts
			w.queue.MarkFailed(job, err.Error())
			return
		}
		w.log.Warn("print failed",
			slog.String("job_id", job.ID),
			slog.String("printer", w.printerID),
			slog.Int("attempt", job.Attempts),
			slog.String("error", err.Error()),
		)
		w.registry.SetStatus(w.printerID, printer.StatusError, err.Error())

		// Calcular backoff y establecer NextRetryAt para evitar Head-of-Line Blocking
		backoff := w.getBackoff(job.Attempts)
		job.NextRetryAt = time.Now().Add(backoff)

		w.queue.MarkFailed(job, err.Error())
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

// getBackoff returns the configured retry delay for attempt n.
// A missing/short slice falls back to 1s.
func (w *Worker) getBackoff(attempt int) time.Duration {
	if len(w.cfg.RetryBackoffMs) == 0 {
		return 1 * time.Second
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
		return 1 * time.Second
	}
	return d
}

// Pool spawns one Worker per registered printer and keeps them aligned
// with hot-reloaded config changes until the agent's root ctx is cancelled.
type Pool struct {
	queue    *Queue
	registry *printer.Registry
	cfg      config.Queue
	log      *slog.Logger

	mu      sync.Mutex
	ctx     context.Context
	workers map[string]context.CancelFunc
	wg      sync.WaitGroup
}

// NewPool returns a worker pool.
func NewPool(q *Queue, reg *printer.Registry, cfg config.Queue, log *slog.Logger) *Pool {
	return &Pool{
		queue:    q,
		registry: reg,
		cfg:      cfg,
		log:      log,
		ctx:      context.Background(),
		workers:  make(map[string]context.CancelFunc),
	}
}

// Start binds the pool to the agent's root context and spawns workers for
// all currently registered printers before returning. Safe to call before
// serving HTTP so hot-reload calls never race with startup.
func (p *Pool) Start(ctx context.Context) {
	p.mu.Lock()
	p.ctx = ctx
	p.mu.Unlock()
	p.Sync()
}

// Wait blocks until all workers have exited.
func (p *Pool) Wait() {
	p.wg.Wait()
}

// Run starts workers for registered printers and blocks until ctx is
// cancelled and all workers exit.
func (p *Pool) Run(ctx context.Context) {
	p.Start(ctx)
	<-ctx.Done()
	p.mu.Lock()
	for id, cancel := range p.workers {
		cancel()
		delete(p.workers, id)
	}
	p.mu.Unlock()
	p.wg.Wait()
}

// Sync reconciles active workers with the printers currently in the registry.
// Workers for removed printers are stopped; workers for newly added printers
// are spawned under the pool's long-lived context (never a request context).
func (p *Pool) Sync() {
	registered := p.registry.Printers()

	p.mu.Lock()
	baseCtx := p.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	for id, cancel := range p.workers {
		if _, ok := registered[id]; !ok {
			cancel()
			delete(p.workers, id)
		}
	}
	if baseCtx.Err() == nil {
		for id := range registered {
			if _, running := p.workers[id]; running {
				continue
			}
			wCtx, cancel := context.WithCancel(baseCtx)
			p.workers[id] = cancel
			p.wg.Add(1)
			go func(printerID string, ctx context.Context) {
				defer p.wg.Done()
				NewWorker(printerID, p.queue, p.registry, p.cfg, p.log).Run(ctx)
			}(id, wCtx)
		}
	}
	p.mu.Unlock()

	if p.queue != nil {
		p.queue.Wake()
	}
}

// Add ensures a Worker is running for printerID under the pool's root context.
func (p *Pool) Add(printerID string) {
	p.mu.Lock()
	if _, running := p.workers[printerID]; running {
		p.mu.Unlock()
		if p.queue != nil {
			p.queue.Wake()
		}
		return
	}
	baseCtx := p.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	if baseCtx.Err() != nil {
		p.mu.Unlock()
		return
	}
	wCtx, cancel := context.WithCancel(baseCtx)
	p.workers[printerID] = cancel
	p.wg.Add(1)
	p.mu.Unlock()

	go func() {
		defer p.wg.Done()
		NewWorker(printerID, p.queue, p.registry, p.cfg, p.log).Run(wCtx)
	}()
	if p.queue != nil {
		p.queue.Wake()
	}
}
