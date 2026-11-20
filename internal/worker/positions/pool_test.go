package positions

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/mock/gomock"

	"github.com/VladimirovArtem/template/internal/infra/telemetry"
	"github.com/VladimirovArtem/template/internal/model"
)

const testBudget = 20 * time.Second

type lifeContextKey struct{}

func currentCandidates(t *testing.T) candidateChecker {
	t.Helper()
	checker := NewMockcandidateChecker(gomock.NewController(t))
	checker.EXPECT().StillStale(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	return checker
}

func poolContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), testBudget)
	t.Cleanup(cancel)
	return ctx
}

func newTestPool(t *testing.T, workers, capacity int, timeout time.Duration) (*Pool, *MockpushGateway) {
	t.Helper()
	gateway := NewMockpushGateway(gomock.NewController(t))
	pool, err := New(gateway, currentCandidates(t), workers, capacity, timeout, slog.New(slog.NewJSONHandler(io.Discard, nil)), testObserver(t), tracenoop.NewTracerProvider().Tracer("test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testBudget)
		defer cancel()
		if err := pool.Shutdown(ctx); err != nil {
			t.Errorf("cleanup pool: %v", err)
		}
		waitPoolDone(t, ctx, pool)
	})
	return pool, gateway
}

func testObserver(t *testing.T) *telemetry.Positions {
	t.Helper()
	observer, err := telemetry.NewPositions(metricnoop.NewMeterProvider())
	if err != nil {
		t.Fatal(err)
	}
	return observer
}

func waitPoolDone(t *testing.T, ctx context.Context, pool *Pool) {
	t.Helper()
	select {
	case <-pool.Done():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func waitSignal(t *testing.T, ctx context.Context, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestPoolSkipsPushWhenRecheckFails(t *testing.T) {
	t.Parallel()
	ctx := poolContext(t)
	pool, _ := newTestPool(t, 1, 1, testBudget)
	checker := NewMockcandidateChecker(gomock.NewController(t))
	pool.checker = checker
	candidate := testTrip(1, time.Now())
	checker.EXPECT().StillStale(gomock.Any(), candidate).Return(false, errors.New("database unavailable"))
	pool.Put(ctx, candidate)
	if err := pool.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if pending, active := pool.Snapshot(); pending != 0 || active != 0 {
		t.Fatalf("after failed recheck: pending=%d active=%d", pending, active)
	}
}

func TestPoolPriorityAndCancel(t *testing.T) {
	t.Parallel()
	ctx := poolContext(t)
	pool, gateway := newTestPool(t, 1, 4, testBudget)
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for _, id := range []byte{3, 1, 2} {
		pool.Put(ctx, testTrip(id, at.Add(time.Duration(id)*time.Second)))
	}
	canceled := testTrip(4, at)
	pool.Put(ctx, canceled)
	if !pool.Cancel(ctx, canceled.ID) || pool.Cancel(ctx, canceled.ID) {
		t.Fatal("Cancel did not remove the waiting task")
	}
	var order []uuid.UUID
	requests := make(map[uuid.UUID]bool)
	gateway.EXPECT().RequestPosition(gomock.Any(), gomock.Any()).Times(3).DoAndReturn(func(got context.Context, request model.PositionRequest) error {
		if got.Value(lifeContextKey{}) != t.Name() {
			t.Error("component context was lost")
		}
		if _, ok := got.Deadline(); !ok {
			t.Error("push request has no deadline")
		}
		if request.RequestID == uuid.Nil || requests[request.RequestID] {
			t.Error("missing or repeated correlation ID")
		}
		requests[request.RequestID] = true
		order = append(order, request.TripID)
		wantDriver := request.TripID
		wantDriver[0] = 1
		if request.DriverID != wantDriver {
			t.Error("wrong driver ID")
		}
		return nil
	})
	if err := pool.Start(context.WithValue(ctx, lifeContextKey{}, t.Name())); err != nil {
		t.Fatal(err)
	}
	if err := pool.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	for i, id := range []byte{1, 2, 3} {
		if len(order) != 3 || order[i] != testTrip(id, at).ID {
			t.Fatalf("push priority order: %v", order)
		}
	}
}

func TestPoolConcurrentDuplicateAndSlowPush(t *testing.T) {
	t.Parallel()
	ctx := poolContext(t)
	pool, gateway := newTestPool(t, 1, 1, testBudget)
	trip := testTrip(1, time.Now())
	queued, duplicates := concurrentPuts(ctx, pool, trip)
	if queued != 1 || duplicates != 31 {
		t.Fatalf("concurrent Put: queued=%d duplicates=%d", queued, duplicates)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	gateway.EXPECT().RequestPosition(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, request model.PositionRequest) error {
		if request.TripID != trip.ID {
			t.Error("unexpected trip")
		}
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err := pool.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, ctx, entered)
	queued, duplicates = concurrentPuts(ctx, pool, trip)
	if queued != 0 || duplicates != 32 || pool.Cancel(ctx, trip.ID) {
		t.Fatalf("in-flight deduplication: queued=%d duplicates=%d", queued, duplicates)
	}
	waiting := testTrip(2, trip.ReferenceAt)
	if result := pool.Put(ctx, waiting); result.Status != Queued || result.Evicted != nil || !pool.Cancel(ctx, waiting.ID) {
		t.Fatalf("slow Push blocked Put/Cancel: %+v", result)
	}
	if ctx.Err() != nil {
		t.Fatal("queue operations waited for the slow Push")
	}
	checkQueueBounds(t, pool)
	close(release)
	if err := pool.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func concurrentPuts(ctx context.Context, pool *Pool, trip model.StaleTrip) (int, int) {
	const count = 32
	start := make(chan struct{})
	results := make(chan PutStatus, count)
	var ready, workers sync.WaitGroup
	ready.Add(count)
	workers.Add(count)
	for range count {
		go func() {
			defer workers.Done()
			ready.Done()
			<-start
			results <- pool.Put(ctx, trip).Status
		}()
	}
	ready.Wait()
	close(start)
	workers.Wait()
	close(results)
	var queued, duplicates int
	for result := range results {
		switch result {
		case Queued:
			queued++
		case Duplicate:
			duplicates++
		}
	}
	return queued, duplicates
}

func TestPoolFixedParallelism(t *testing.T) {
	t.Parallel()
	const workers, total = 3, 12
	ctx := poolContext(t)
	pool, gateway := newTestPool(t, workers, total, testBudget)
	at := time.Now()
	for id := byte(1); id <= total; id++ {
		pool.Put(ctx, testTrip(id, at))
	}
	entered := make(chan struct{}, total)
	release := make(chan struct{})
	var mu sync.Mutex
	active, peak := 0, 0
	seen := make(map[uuid.UUID]int)
	gateway.EXPECT().RequestPosition(gomock.Any(), gomock.Any()).Times(total).DoAndReturn(func(ctx context.Context, request model.PositionRequest) error {
		mu.Lock()
		active++
		peak = max(peak, active)
		seen[request.TripID]++
		mu.Unlock()
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		mu.Lock()
		active--
		mu.Unlock()
		return ctx.Err()
	})
	if err := pool.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.Start(ctx); err == nil {
		t.Fatal("second Start was accepted")
	}
	for range workers {
		waitSignal(t, ctx, entered)
	}
	checkQueueBounds(t, pool)
	close(release)
	if err := pool.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if peak != workers || active != 0 || len(seen) != total {
		t.Fatalf("parallelism: peak=%d active=%d unique=%d", peak, active, len(seen))
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("trip %s processed %d times", id, count)
		}
	}
}

func TestPoolContinuesAndReleasesTripAfterFailure(t *testing.T) {
	t.Parallel()
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "timeout"}[timeout], func(t *testing.T) {
			t.Parallel()
			ctx := poolContext(t)
			requestTimeout := testBudget
			if timeout {
				requestTimeout = 20 * time.Millisecond
			}
			pool, gateway := newTestPool(t, 1, 2, requestTimeout)
			at := time.Now()
			first, marker := testTrip(1, at), testTrip(2, at.Add(time.Second))
			pool.Put(ctx, first)
			pool.Put(ctx, marker)
			markerEntered, releaseMarker := make(chan struct{}), make(chan struct{})
			firstAttempts := 0
			gateway.EXPECT().RequestPosition(gomock.Any(), gomock.Any()).Times(3).DoAndReturn(func(ctx context.Context, request model.PositionRequest) error {
				if request.TripID == marker.ID {
					close(markerEntered)
					select {
					case <-releaseMarker:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				firstAttempts++
				if firstAttempts == 1 {
					if timeout {
						<-ctx.Done()
						if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
							t.Error("push was canceled instead of timing out")
						}
						return ctx.Err()
					}
					return errors.New("push failed")
				}
				return nil
			})
			if err := pool.Start(ctx); err != nil {
				t.Fatal(err)
			}
			waitSignal(t, ctx, markerEntered)
			if result := pool.Put(ctx, first); result.Status != Queued {
				t.Fatalf("failed trip still busy: %+v", result)
			}
			close(releaseMarker)
			if err := pool.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			if firstAttempts != 2 {
				t.Fatalf("first trip attempts=%d", firstAttempts)
			}
		})
	}
}

func TestPoolConcurrentPutCancelTakeAndShutdown(t *testing.T) {
	t.Parallel()
	ctx := poolContext(t)
	pool, gateway := newTestPool(t, 4, 8, testBudget)
	at := time.Now()
	trips := make([]model.StaleTrip, 32)
	for i := range trips {
		trips[i] = testTrip(byte(i+1), at.Add(time.Duration(i)*time.Second))
	}
	var mu sync.Mutex
	active := make(map[uuid.UUID]bool)
	gateway.EXPECT().RequestPosition(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(_ context.Context, request model.PositionRequest) error {
		mu.Lock()
		if active[request.TripID] || len(active) >= pool.workers {
			t.Error("concurrent double processing or excess parallelism")
		}
		active[request.TripID] = true
		mu.Unlock()
		runtime.Gosched()
		mu.Lock()
		delete(active, request.TripID)
		mu.Unlock()
		return nil
	})
	if err := pool.Start(ctx); err != nil {
		t.Fatal(err)
	}
	start, closing := make(chan struct{}), make(chan struct{}, 1)
	var producers sync.WaitGroup
	for producer := range 8 {
		producers.Add(1)
		go func() {
			defer producers.Done()
			<-start
			for i := range 300 {
				trip := trips[(i+producer)%len(trips)]
				result := pool.Put(ctx, trip)
				switch result.Status {
				case Queued, Duplicate, Dropped, Closed:
				default:
					t.Errorf("unknown Put result: %+v", result)
				}
				pool.Cancel(ctx, trips[(i+producer+1)%len(trips)].ID)
				checkQueueBounds(t, pool)
				if producer == 0 && i == 50 {
					closing <- struct{}{}
				}
			}
		}()
	}
	close(start)
	waitSignal(t, ctx, closing)
	if err := pool.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	producers.Wait()
	if pool.Put(ctx, trips[0]).Status != Closed || len(active) != 0 {
		t.Fatal("pool kept work after shutdown")
	}
	checkQueueBounds(t, pool)
}

func checkQueueBounds(t *testing.T, pool *Pool) {
	t.Helper()
	q := pool.queue
	q.mu.Lock()
	valid := len(q.pending) <= q.capacity && cap(q.pending) <= q.capacity && len(q.waiting) == len(q.pending) && len(q.inFlight) <= pool.workers
	for i, item := range q.pending {
		_, busy := q.inFlight[item.trip.ID]
		valid = valid && !busy && item.index == i && q.waiting[item.trip.ID] == item
	}
	q.mu.Unlock()
	if !valid {
		t.Error("queue capacity, heap index or in-flight invariant violated")
	}
}
