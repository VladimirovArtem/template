package positions

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"

	"github.com/VladimirovArtem/template/internal/infra/telemetry"
	"github.com/VladimirovArtem/template/internal/model"
)

type scanSignals struct {
	*telemetry.Positions
	completed chan int
	requests  chan string
}

func (s *scanSignals) Requested(ctx context.Context, result string, duration time.Duration) {
	s.Positions.Requested(ctx, result, duration)
	if s.requests != nil {
		s.requests <- result
	}
}

func (s *scanSignals) RowsRead(ctx context.Context, count int) {
	s.Positions.RowsRead(ctx, count)
	s.completed <- count
}

type scanHarness struct {
	ctx      context.Context
	pool     *Pool
	worker   *Worker
	repo     *MockstaleRepository
	gateway  *MockpushGateway
	signals  *scanSignals
	ticks    chan time.Time
	reader   *sdkmetric.ManualReader
	exporter *tracetest.InMemoryExporter
	traces   *sdktrace.TracerProvider
	meters   *sdkmetric.MeterProvider
	now      time.Time
	nowCalls atomic.Int64
}

func newScanHarness(t *testing.T, capacity int, gatewayFactory ...func(*scanHarness) pushGateway) *scanHarness {
	t.Helper()
	h := &scanHarness{ctx: poolContext(t), ticks: make(chan time.Time, 1), now: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
	h.exporter = tracetest.NewInMemoryExporter()
	h.traces = sdktrace.NewTracerProvider(sdktrace.WithSyncer(h.exporter), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	h.reader = sdkmetric.NewManualReader()
	h.meters = sdkmetric.NewMeterProvider(sdkmetric.WithReader(h.reader))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testBudget)
		defer cancel()
		if err := h.traces.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		if err := h.meters.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	events, err := telemetry.NewPositions(h.meters)
	if err != nil {
		t.Fatal(err)
	}
	h.signals = &scanSignals{Positions: events, completed: make(chan int, 16)}
	ctrl := gomock.NewController(t)
	h.repo, h.gateway = NewMockstaleRepository(ctrl), NewMockpushGateway(ctrl)
	ticker := NewMockTicker(ctrl)
	ticker.EXPECT().Ticks().Return(h.ticks).Times(1)
	ticker.EXPECT().Stop().Times(1)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	var gateway pushGateway = h.gateway
	if len(gatewayFactory) != 0 {
		gateway = gatewayFactory[0](h)
	}
	h.pool, err = New(gateway, currentCandidates(t), 1, capacity, testBudget, logger, h.signals, h.traces.Tracer("test"))
	if err != nil {
		t.Fatal(err)
	}
	if err := telemetry.RegisterPositionState(h.meters, h.pool); err != nil {
		t.Fatal(err)
	}
	h.worker, err = NewWorker(h.repo, h.pool, ScanConfig{Interval: time.Second, StaleAfter: time.Minute, BatchSize: 100},
		func(d time.Duration) Ticker {
			if d != time.Second {
				t.Error("wrong ticker interval")
			}
			return ticker
		}, func() time.Time { return h.now.Add(time.Duration(h.nowCalls.Add(1)-1) * time.Second) }, h.signals, h.traces.Tracer("test"), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testBudget)
		defer cancel()
		if err := h.worker.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		select {
		case <-h.worker.Done():
		case <-ctx.Done():
			t.Error(ctx.Err())
		}
	})
	if err := h.worker.Start(h.ctx); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *scanHarness) scan(t *testing.T) int {
	t.Helper()
	select {
	case h.ticks <- h.now:
	case <-h.ctx.Done():
		t.Fatal(h.ctx.Err())
	}
	select {
	case rows := <-h.signals.completed:
		return rows
	case <-h.ctx.Done():
		t.Fatal(h.ctx.Err())
	}
	return -1
}

func (h *scanHarness) blockPush(t *testing.T) (chan struct{}, *atomic.Int64) {
	t.Helper()
	entered, release := make(chan struct{}), make(chan struct{})
	var processed atomic.Int64
	primer := testTrip(255, h.now.Add(-time.Hour))
	h.gateway.EXPECT().RequestPosition(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, request model.PositionRequest) error {
		if request.TripID == primer.ID {
			close(entered)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		processed.Add(1)
		return nil
	})
	if h.pool.Put(h.ctx, primer).Status != Queued {
		t.Fatal("primer not queued")
	}
	waitSignal(t, h.ctx, entered)
	return release, &processed
}

func scanTrips(now time.Time, count int) []model.StaleTrip {
	trips := make([]model.StaleTrip, 0, count)
	for i := 1; i <= count; i++ {
		trips = append(trips, testTrip(byte(i), now.Add(-2*time.Minute)))
	}
	return trips
}

func TestScannerBatchesAndDuplicates(t *testing.T) {
	t.Parallel()
	h := newScanHarness(t, 300)
	release, processed := h.blockPush(t)
	trips := scanTrips(h.now, 250)
	var calls atomic.Int64
	h.repo.EXPECT().ListStaleTrips(gomock.Any(), gomock.Any(), time.Minute, 100, gomock.Any()).Times(6).DoAndReturn(
		func(ctx context.Context, at time.Time, _ time.Duration, _ int, cursor *model.StaleTripCursor) ([]model.StaleTrip, error) {
			call := int(calls.Add(1) - 1)
			page, walk := call%3, call/3
			if !at.Equal(h.now.Add(time.Duration(walk)*time.Second)) || !trace.SpanContextFromContext(ctx).IsValid() {
				t.Error("check time or scan span lost")
			}
			if page == 0 {
				if cursor != nil {
					t.Error("new tick did not reset cursor")
				}
			} else {
				last := trips[page*100-1]
				if cursor == nil || cursor.ID != last.ID || !cursor.ReferenceAt.Equal(last.ReferenceAt) {
					t.Error("wrong continuation cursor")
				}
			}
			return trips[page*100 : min(page*100+100, len(trips))], nil
		})
	if rows := h.scan(t); rows != 250 || calls.Load() != 3 || h.nowCalls.Load() != 1 {
		t.Fatalf("first walk: rows=%d queries=%d now=%d", rows, calls.Load(), h.nowCalls.Load())
	}
	metrics := readPositionMetrics(t, h.ctx, h.reader)
	if countResult(metrics, "tripgo.stale_checks", "queued") != 250 || gaugeValue(metrics, "tripgo.position_requests.queue.size") != 250 || gaugeValue(metrics, "tripgo.position_requests.workers.active") != 1 {
		t.Fatal("wrong batch/queue metrics")
	}
	if rows := h.scan(t); rows != 250 || calls.Load() != 6 || h.nowCalls.Load() != 2 {
		t.Fatalf("duplicate walk: rows=%d queries=%d", rows, calls.Load())
	}
	metrics = readPositionMetrics(t, h.ctx, h.reader)
	if countResult(metrics, "tripgo.stale_checks", "duplicate") != 250 {
		t.Fatal("duplicates stopped scan")
	}
	rows := metrics["tripgo.stale_checks.rows_read"].Data.(metricdata.Histogram[int64]).DataPoints
	if len(rows) != 1 || rows[0].Count != 2 || rows[0].Sum != 500 {
		t.Fatalf("rows_read: %+v", rows)
	}
	close(release)
	if err := h.worker.Shutdown(h.ctx); err != nil {
		t.Fatal(err)
	}
	if processed.Load() != 250 {
		t.Fatalf("processed=%d", processed.Load())
	}
	metrics = readPositionMetrics(t, h.ctx, h.reader)
	if gaugeValue(metrics, "tripgo.position_requests.queue.size") != 0 || gaugeValue(metrics, "tripgo.position_requests.workers.active") != 0 || countResult(metrics, "tripgo.position_requests", "success") != 251 {
		t.Fatal("drain metrics did not match actual requests")
	}
	t.Log("250 trips: three queries per walk, rows_read=250; repeated tick: 250 duplicates; drain processed all 250 once")
}

func TestScannerSkipsMovedTripUntilNextTick(t *testing.T) {
	t.Parallel()
	h := newScanHarness(t, 1)
	h.worker.scanner.config.BatchSize = 1
	h.signals.requests = make(chan string, 2)
	first := testTrip(1, h.now.Add(-3*time.Hour))
	moved := first
	moved.ReferenceAt = h.now.Add(-2 * time.Hour)
	firstCursor := &model.StaleTripCursor{ReferenceAt: first.ReferenceAt, ID: first.ID}
	movedCursor := &model.StaleTripCursor{ReferenceAt: moved.ReferenceAt, ID: moved.ID}
	next := h.now.Add(time.Second)
	list := h.repo.EXPECT()
	gomock.InOrder(
		list.ListStaleTrips(gomock.Any(), h.now, time.Minute, 1, nil).Return([]model.StaleTrip{first}, nil),
		list.ListStaleTrips(gomock.Any(), h.now, time.Minute, 1, firstCursor).DoAndReturn(
			func(ctx context.Context, _ time.Time, _ time.Duration, _ int, _ *model.StaleTripCursor) ([]model.StaleTrip, error) {
				select {
				case <-h.signals.requests:
					return []model.StaleTrip{moved}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}),
		list.ListStaleTrips(gomock.Any(), h.now, time.Minute, 1, movedCursor).Return(nil, nil),
		list.ListStaleTrips(gomock.Any(), next, time.Minute, 1, nil).Return([]model.StaleTrip{moved}, nil),
		list.ListStaleTrips(gomock.Any(), next, time.Minute, 1, movedCursor).Return(nil, nil),
	)
	h.gateway.EXPECT().RequestPosition(gomock.Any(), gomock.Any()).Return(nil).Times(2)
	if rows := h.scan(t); rows != 2 {
		t.Fatalf("first tick read %d rows, want 2", rows)
	}
	metrics := readPositionMetrics(t, h.ctx, h.reader)
	if countResult(metrics, "tripgo.stale_checks", "queued") != 1 || countResult(metrics, "tripgo.stale_checks", "duplicate") != 1 {
		t.Fatal("moved trip was queued again within the same tick")
	}
	if rows := h.scan(t); rows != 1 {
		t.Fatalf("next tick read %d rows, want 1", rows)
	}
	if err := h.worker.Shutdown(h.ctx); err != nil {
		t.Fatal(err)
	}
}

func TestScannerOverflowStopsAndRestarts(t *testing.T) {
	t.Parallel()
	h := newScanHarness(t, 50)
	release, _ := h.blockPush(t)
	h.repo.EXPECT().ListStaleTrips(gomock.Any(), gomock.Any(), time.Minute, 100, nil).Times(2).Return(scanTrips(h.now, 100), nil)
	for range 2 {
		if rows := h.scan(t); rows != 100 {
			t.Fatalf("rows=%d", rows)
		}
	}
	metrics := readPositionMetrics(t, h.ctx, h.reader)
	if countResult(metrics, "tripgo.stale_checks", "queued") != 50 || countResult(metrics, "tripgo.stale_checks", "duplicate") != 50 || countResult(metrics, "tripgo.stale_checks", "dropped") != 2 {
		t.Fatal("wrong overflow accounting")
	}
	rows := metrics["tripgo.stale_checks.rows_read"].Data.(metricdata.Histogram[int64]).DataPoints[0]
	if rows.Count != 2 || rows.Sum != 200 || gaugeValue(metrics, "tripgo.position_requests.queue.size") != 50 {
		t.Fatalf("overflow rows/gauge: %+v", rows)
	}
	close(release)
	t.Log("queue 50/batch 100: one query per tick, first tick 50 queued + 1 dropped, rows_read=100; next tick starts at nil cursor")
}

func TestScannerEvictionEndsWalk(t *testing.T) {
	t.Parallel()
	h := newScanHarness(t, 2)
	release, _ := h.blockPush(t)
	h.pool.Put(h.ctx, testTrip(252, h.now))
	h.pool.Put(h.ctx, testTrip(253, h.now.Add(time.Second)))
	h.repo.EXPECT().ListStaleTrips(gomock.Any(), h.now, time.Minute, 100, nil).Return(scanTrips(h.now, 100), nil)
	if h.scan(t) != 100 {
		t.Fatal("full batch was not counted")
	}
	metrics := readPositionMetrics(t, h.ctx, h.reader)
	if countResult(metrics, "tripgo.stale_checks", "queued") != 1 || countResult(metrics, "tripgo.stale_checks", "dropped") != 1 || countResult(metrics, "tripgo.position_requests", "cancelled") != 0 {
		t.Fatal("eviction was not queued+dropped")
	}
	close(release)
}

func TestScannerSerialAndRecoversAfterDatabaseError(t *testing.T) {
	t.Parallel()
	h := newScanHarness(t, 2)
	entered, release := make(chan struct{}), make(chan struct{})
	var active, peak atomic.Int64
	first := h.repo.EXPECT().ListStaleTrips(gomock.Any(), h.now, time.Minute, 100, nil).DoAndReturn(func(ctx context.Context, _ time.Time, _ time.Duration, _ int, _ *model.StaleTripCursor) ([]model.StaleTrip, error) {
		current := active.Add(1)
		peak.Store(current)
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
		active.Add(-1)
		return nil, errors.New("database unavailable")
	})
	h.repo.EXPECT().ListStaleTrips(gomock.Any(), h.now.Add(time.Second), time.Minute, 100, nil).After(first).DoAndReturn(func(context.Context, time.Time, time.Duration, int, *model.StaleTripCursor) ([]model.StaleTrip, error) {
		if active.Load() != 0 {
			t.Error("parallel scans")
		}
		return nil, nil
	})
	h.ticks <- h.now
	waitSignal(t, h.ctx, entered)
	h.ticks <- h.now
	close(release)
	for range 2 {
		select {
		case n := <-h.signals.completed:
			if n != 0 {
				t.Error("failed/empty scan fabricated rows")
			}
		case <-h.ctx.Done():
			t.Fatal(h.ctx.Err())
		}
	}
	if peak.Load() != 1 || h.nowCalls.Load() != 2 {
		t.Fatal("scanner overlapped checks or failed to recover")
	}
}

func TestWorkerCancellationStopsDatabaseAndIdleWorkers(t *testing.T) {
	t.Parallel()
	h := newScanHarness(t, 2)
	entered, canceled := make(chan struct{}), make(chan struct{})
	h.repo.EXPECT().ListStaleTrips(gomock.Any(), h.now, time.Minute, 100, nil).DoAndReturn(func(ctx context.Context, _ time.Time, _ time.Duration, _ int, _ *model.StaleTripCursor) ([]model.StaleTrip, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	})
	h.ticks <- h.now
	waitSignal(t, h.ctx, entered)
	if err := h.worker.Shutdown(h.ctx); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, h.ctx, canceled)
	if n := <-h.signals.completed; n != 0 {
		t.Fatal("cancelled scan fabricated rows")
	}
	metrics := readPositionMetrics(t, h.ctx, h.reader)
	if countResult(metrics, "tripgo.stale_checks", "dropped") != 0 || countResult(metrics, "tripgo.position_requests", "cancelled") != 0 {
		t.Fatal("shutdown fabricated business outcomes")
	}
}

func readPositionMetrics(t *testing.T, ctx context.Context, reader *sdkmetric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()
	var output metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &output); err != nil {
		t.Fatal(err)
	}
	metrics := make(map[string]metricdata.Metrics)
	for _, scope := range output.ScopeMetrics {
		for _, item := range scope.Metrics {
			metrics[item.Name] = item
		}
	}
	return metrics
}

func countResult(metrics map[string]metricdata.Metrics, name, result string) int64 {
	item, ok := metrics[name]
	if !ok {
		return 0
	}
	var count int64
	for _, point := range item.Data.(metricdata.Sum[int64]).DataPoints {
		value, _ := point.Attributes.Value("result")
		if value.AsString() == result {
			count += point.Value
		}
	}
	return count
}

func gaugeValue(metrics map[string]metricdata.Metrics, name string) int64 {
	item, ok := metrics[name]
	if !ok {
		return -1
	}
	points := item.Data.(metricdata.Gauge[int64]).DataPoints
	if len(points) != 1 {
		return -1
	}
	return points[0].Value
}
