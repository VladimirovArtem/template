package positions

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"
)

type Worker struct {
	scanner   scanner
	pool      *Pool
	newTicker func(time.Duration) Ticker
	mu        sync.Mutex
	started   bool
	closed    bool
	cancel    context.CancelFunc
	stopScan  context.CancelFunc
	scanDone  chan struct{}
	done      chan struct{}
}

func NewWorker(repo staleRepository, pool *Pool, cfg ScanConfig, newTicker func(time.Duration) Ticker, now func() time.Time, observer scanObserver, tracer trace.Tracer, logger *slog.Logger) (*Worker, error) {
	if repo == nil || pool == nil || newTicker == nil || now == nil || observer == nil || tracer == nil || logger == nil {
		return nil, errors.New("position scanner dependencies must be set")
	}
	if cfg.Interval <= 0 || cfg.StaleAfter <= 0 || cfg.StaleAfter%time.Microsecond != 0 || cfg.BatchSize <= 0 {
		return nil, errors.New("position scan interval and batch size must be positive; stale duration must be positive whole microseconds")
	}
	return &Worker{
		scanner: scanner{repo: repo, pool: pool, config: cfg, now: now, observer: observer, tracer: tracer, logger: logger},
		pool:    pool, newTicker: newTicker, scanDone: make(chan struct{}), done: make(chan struct{}),
	}, nil
}

func (w *Worker) Start(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started || w.closed {
		return errors.New("position worker already started or shut down")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ticker := w.newTicker(w.scanner.config.Interval)
	if ticker == nil {
		return errors.New("position ticker must be set")
	}
	life, cancel := context.WithCancel(ctx)
	if err := w.pool.Start(life); err != nil {
		ticker.Stop()
		cancel()
		return err
	}
	scanCtx, stopScan := context.WithCancel(life)
	w.cancel, w.stopScan, w.started = cancel, stopScan, true
	go func() {
		w.scanner.run(scanCtx, ticker)
		close(w.scanDone)
		<-w.pool.Done()
		cancel()
		close(w.done)
	}()
	return nil
}

func (w *Worker) StopScanner(ctx context.Context) error {
	w.mu.Lock()
	if !w.started {
		w.mu.Unlock()
		return nil
	}
	stop := w.stopScan
	w.mu.Unlock()
	stop()
	select {
	case <-w.scanDone:
		return nil
	default:
	}
	select {
	case <-w.scanDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Worker) Shutdown(ctx context.Context) error {
	select {
	case <-w.done:
		return nil
	default:
	}
	w.mu.Lock()
	if !w.started {
		if !w.closed {
			w.closed = true
			w.pool.Abort()
			close(w.scanDone)
			close(w.done)
		}
		w.mu.Unlock()
		return nil
	}
	w.mu.Unlock()
	scanErr := w.StopScanner(ctx)
	poolErr := w.pool.Shutdown(ctx)
	select {
	case <-w.done:
		return errors.Join(scanErr, poolErr)
	case <-ctx.Done():
		w.Abort()
		return errors.Join(scanErr, poolErr, ctx.Err())
	}
}

func (w *Worker) Abort() {
	w.mu.Lock()
	if !w.started {
		if !w.closed {
			w.closed = true
			w.pool.Abort()
			close(w.scanDone)
			close(w.done)
		}
		w.mu.Unlock()
		return
	}
	cancel := w.cancel
	w.mu.Unlock()
	cancel()
	w.pool.Abort()
}

func (w *Worker) Done() <-chan struct{} { return w.done }
