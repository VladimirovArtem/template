package positions

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/mock/gomock"

	"github.com/VladimirovArtem/template/internal/model"
)

func TestPoolInvalidConfiguration(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name                string
		workers, capacity   int
		timeout             time.Duration
		noGateway, noLogger bool
	}{
		{name: "zero workers", capacity: 1, timeout: time.Second},
		{name: "negative workers", workers: -1, capacity: 1, timeout: time.Second},
		{name: "zero capacity", workers: 1, timeout: time.Second},
		{name: "negative capacity", workers: 1, capacity: -1, timeout: time.Second},
		{name: "zero timeout", workers: 1, capacity: 1},
		{name: "negative timeout", workers: 1, capacity: 1, timeout: -time.Second},
		{name: "nil gateway", workers: 1, capacity: 1, timeout: time.Second, noGateway: true},
		{name: "nil logger", workers: 1, capacity: 1, timeout: time.Second, noLogger: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var gateway pushGateway = NewMockpushGateway(gomock.NewController(t))
			logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
			if tt.noGateway {
				gateway = nil
			}
			if tt.noLogger {
				logger = nil
			}
			if pool, err := New(gateway, currentCandidates(t), tt.workers, tt.capacity, tt.timeout, logger, testObserver(t), tracenoop.NewTracerProvider().Tracer("test")); err == nil || pool != nil {
				t.Fatalf("New = %+v, %v", pool, err)
			}
		})
	}
}

func TestPoolShutdownBeforeStart(t *testing.T) {
	t.Parallel()
	ctx := poolContext(t)
	pool, _ := newTestPool(t, 2, 1, testBudget)
	trip := testTrip(1, time.Now())
	pool.Put(ctx, trip)
	for range 2 {
		if err := pool.Shutdown(ctx); err != nil {
			t.Fatal(err)
		}
	}
	waitPoolDone(t, ctx, pool)
	if pool.Put(ctx, trip).Status != Closed || pool.Start(ctx) == nil || len(pool.queue.pending) != 0 {
		t.Fatal("pool restarted after shutdown or kept unstarted work")
	}
}

func TestPoolStartWithCanceledContext(t *testing.T) {
	t.Parallel()
	pool, _ := newTestPool(t, 1, 1, testBudget)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := pool.Start(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Start = %v", err)
	}
}

func TestPoolGracefulShutdown(t *testing.T) {
	t.Parallel()
	ctx := poolContext(t)
	pool, gateway := newTestPool(t, 1, 3, testBudget)
	at := time.Now()
	for _, id := range []byte{1, 3, 2} {
		pool.Put(ctx, testTrip(id, at))
	}
	entered, release := make(chan struct{}), make(chan struct{})
	order := make(chan byte, 3)
	gateway.EXPECT().RequestPosition(gomock.Any(), gomock.Any()).Times(3).DoAndReturn(func(ctx context.Context, request model.PositionRequest) error {
		order <- request.TripID[15]
		if request.TripID[15] == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})
	if err := pool.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, ctx, entered)
	stopped := make(chan error, 1)
	go func() { stopped <- pool.Shutdown(ctx) }()
	waitAdmissionClosed(t, ctx, pool.queue)
	if result := pool.Put(ctx, testTrip(4, at)); result.Status != Closed {
		t.Fatalf("new work accepted during drain: %+v", result)
	}
	close(release)
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	waitPoolDone(t, ctx, pool)
	for _, want := range []byte{1, 2, 3} {
		if got := <-order; got != want {
			t.Fatalf("drain order: got %d, want %d", got, want)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if pool.Shutdown(canceled) != nil || pool.Start(ctx) == nil {
		t.Fatal("repeated shutdown or Start after shutdown is unsafe")
	}
}

func waitAdmissionClosed(t *testing.T, ctx context.Context, q *queue) {
	t.Helper()
	stopWake := context.AfterFunc(ctx, func() {
		q.mu.Lock()
		q.ready.Broadcast()
		q.mu.Unlock()
	})
	defer stopWake()
	q.mu.Lock()
	for !q.closed && ctx.Err() == nil {
		q.ready.Wait()
	}
	closed := q.closed
	q.mu.Unlock()
	if !closed {
		t.Fatal("shutdown did not close admission")
	}
}

func TestPoolForcedShutdownKeepsInFlightUntilReturn(t *testing.T) {
	t.Parallel()
	ctx := poolContext(t)
	pool, gateway := newTestPool(t, 1, 2, testBudget)
	at := time.Now()
	trip := testTrip(1, at)
	pool.Put(ctx, trip)
	pool.Put(ctx, testTrip(2, at))
	entered, canceled, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	gateway.EXPECT().RequestPosition(gomock.Any(), gomock.Any()).DoAndReturn(func(requestCtx context.Context, _ model.PositionRequest) error {
		close(entered)
		<-requestCtx.Done()
		close(canceled)
		select {
		case <-finish:
		case <-ctx.Done():
		}
		return requestCtx.Err()
	})
	if err := pool.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, ctx, entered)
	budget, cancel := context.WithCancel(ctx)
	cancel()
	if err := pool.Shutdown(budget); !errors.Is(err, context.Canceled) {
		t.Fatalf("forced shutdown: %v", err)
	}
	waitSignal(t, ctx, canceled)
	pool.queue.mu.Lock()
	pending, active := len(pool.queue.pending), len(pool.queue.inFlight)
	pool.queue.mu.Unlock()
	if pending != 0 || active != 1 || pool.Cancel(ctx, trip.ID) || pool.Put(ctx, trip).Status != Closed {
		t.Fatalf("forced stop: pending=%d in-flight=%d", pending, active)
	}
	select {
	case <-pool.Done():
		t.Fatal("Done closed before gateway returned")
	default:
	}
	close(finish)
	waitPoolDone(t, ctx, pool)
	if len(pool.queue.inFlight) != 0 {
		t.Fatal("completed request still in-flight")
	}
}

func TestPoolShutdownDeadline(t *testing.T) {
	t.Parallel()
	ctx := poolContext(t)
	pool, gateway := newTestPool(t, 1, 2, testBudget)
	at := time.Now()
	pool.Put(ctx, testTrip(1, at))
	pool.Put(ctx, testTrip(2, at))
	entered := make(chan struct{})
	gateway.EXPECT().RequestPosition(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ model.PositionRequest) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	})
	if err := pool.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, ctx, entered)
	budget, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := pool.Shutdown(budget); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown deadline: %v", err)
	}
	waitPoolDone(t, ctx, pool)
	checkQueueBounds(t, pool)
}

func TestPoolParentCancellation(t *testing.T) {
	t.Parallel()
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle workers", true: "in-flight request"}[active], func(t *testing.T) {
			t.Parallel()
			ctx := poolContext(t)
			pool, gateway := newTestPool(t, 3, 2, testBudget)
			workCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			entered := make(chan struct{})
			if active {
				pool.Put(ctx, testTrip(1, time.Now()))
				gateway.EXPECT().RequestPosition(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ model.PositionRequest) error {
					close(entered)
					<-ctx.Done()
					return ctx.Err()
				})
			}
			if err := pool.Start(workCtx); err != nil {
				t.Fatal(err)
			}
			if active {
				waitSignal(t, ctx, entered)
			}
			cancel()
			waitPoolDone(t, ctx, pool)
			if pool.Put(ctx, testTrip(2, time.Now())).Status != Closed || pool.Shutdown(ctx) != nil {
				t.Fatal("parent cancellation did not close the pool")
			}
			checkQueueBounds(t, pool)
		})
	}
}
