package positions

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/VladimirovArtem/template/internal/logging"
	"github.com/VladimirovArtem/template/internal/model"
)

type Pool struct {
	queue    *queue
	gateway  pushGateway
	checker  candidateChecker
	workers  int
	timeout  time.Duration
	logger   *slog.Logger
	observer requestObserver
	tracer   trace.Tracer
	mu       sync.Mutex
	started  bool
	closed   bool
	cancel   context.CancelFunc
	done     chan struct{}
}

func New(gateway pushGateway, checker candidateChecker, workers, capacity int, timeout time.Duration, logger *slog.Logger, observer requestObserver, tracer trace.Tracer) (*Pool, error) {
	if gateway == nil || checker == nil || logger == nil || observer == nil || tracer == nil {
		return nil, errors.New("position gateway, candidate checker, logger, observer and tracer must be set")
	}
	if workers <= 0 || capacity <= 0 || timeout <= 0 {
		return nil, errors.New("position workers, queue capacity and timeout must be positive")
	}
	return &Pool{
		queue: newQueue(capacity), gateway: gateway, checker: checker, workers: workers,
		timeout: timeout, logger: logger, observer: observer, tracer: tracer, done: make(chan struct{}),
	}, nil
}

func (p *Pool) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started || p.closed {
		return errors.New("position pool already started or shut down")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("start position workers: %w", err)
	}
	ctx, p.cancel = context.WithCancel(ctx)
	p.started = true
	exited := make(chan struct{}, p.workers)
	for range p.workers {
		go func() {
			defer func() { exited <- struct{}{} }()
			p.work(ctx)
		}()
	}
	go p.supervise(ctx, exited)
	p.logger.InfoContext(ctx, "position workers started", "workers", p.workers)
	return nil
}

func (p *Pool) Put(ctx context.Context, trip model.StaleTrip) PutResult {
	return p.queue.put(trip, trace.SpanContextFromContext(ctx))
}

func (p *Pool) Cancel(ctx context.Context, id uuid.UUID) bool {
	removed := p.queue.cancel(id)
	if removed {
		p.observer.Canceled(ctx)
	}
	return removed
}

func (p *Pool) Snapshot() (pending, active int) { return p.queue.snapshot() }
func (p *Pool) Done() <-chan struct{}           { return p.done }

func (p *Pool) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	p.queue.close()
	if !p.started {
		if !p.closed {
			p.queue.stop()
			close(p.done)
		}
		p.closed = true
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()
	select {
	case <-p.done:
		return nil
	default:
	}
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		p.Abort()
		return ctx.Err()
	}
}

func (p *Pool) Abort() {
	p.mu.Lock()
	p.queue.stop()
	if !p.started && !p.closed {
		close(p.done)
	}
	p.closed = true
	cancel := p.cancel
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (p *Pool) supervise(ctx context.Context, exited <-chan struct{}) {
	canceled := ctx.Done()
	for remaining := p.workers; remaining > 0; {
		select {
		case <-canceled:
			p.queue.stop()
			canceled = nil
		case <-exited:
			remaining--
		}
	}
	p.queue.stop()
	p.cancel()
	p.logger.InfoContext(context.WithoutCancel(ctx), "position workers stopped")
	close(p.done)
}

func (p *Pool) work(ctx context.Context) {
	for {
		trip, ok := p.queue.take()
		if !ok {
			return
		}
		if ctx.Err() != nil {
			p.queue.complete(trip.ID)
			return
		}
		p.request(ctx, trip)
	}
}

func (p *Pool) request(ctx context.Context, trip task) {
	options := []trace.SpanStartOption{trace.WithNewRoot()}
	if trip.check.IsValid() {
		options = append(options, trace.WithLinks(trace.Link{SpanContext: trip.check}))
	}
	ctx, span := p.tracer.Start(ctx, "positions.request", options...)
	defer span.End()
	requestCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	if !p.shouldSend(requestCtx, span, trip.StaleTrip) {
		p.queue.complete(trip.ID)
		return
	}
	input := model.PositionRequest{RequestID: uuid.New(), TripID: trip.ID, DriverID: trip.DriverID}
	started := time.Now()
	err := p.gateway.RequestPosition(requestCtx, input)
	duration := time.Since(started)
	p.queue.complete(trip.ID)
	result := "success"
	if err != nil {
		result = "error"
		span.SetStatus(codes.Error, "position push failed")
		span.RecordError(logging.SafeError{Cause: err})
		span.SetAttributes(attribute.String("error.type", logging.ErrorType(err)))
		var status interface{ HTTPStatus() int }
		if errors.As(err, &status) {
			span.SetAttributes(attribute.Int("http.response.status_code", status.HTTPStatus()))
		}
	}
	p.observer.Requested(ctx, result, duration)
	if err != nil && ctx.Err() == nil {
		attrs := append(logging.ErrorAttrs(err), slog.String("trip_id", trip.ID.String()), slog.String("request_id", input.RequestID.String()))
		var status interface{ HTTPStatus() int }
		if errors.As(err, &status) {
			attrs = append(attrs, slog.Int("http.status_code", status.HTTPStatus()))
		}
		p.logger.LogAttrs(ctx, slog.LevelWarn, "position request failed", attrs...)
	}
}

func (p *Pool) shouldSend(ctx context.Context, span trace.Span, trip model.StaleTrip) bool {
	current, err := p.checker.StillStale(ctx, trip)
	if err != nil {
		span.SetStatus(codes.Error, "stale trip recheck failed")
		span.RecordError(logging.SafeError{Cause: err})
		if !errors.Is(err, context.Canceled) {
			attrs := append(logging.ErrorAttrs(err), slog.String("trip_id", trip.ID.String()))
			p.logger.LogAttrs(ctx, slog.LevelWarn, "stale trip recheck failed", attrs...)
		}
		return false
	}
	if !current {
		p.observer.Canceled(ctx)
	}
	return current && ctx.Err() == nil
}
