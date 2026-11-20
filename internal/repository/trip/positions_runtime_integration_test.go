//go:build integration

package trip_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/VladimirovArtem/template/api"
	"github.com/VladimirovArtem/template/internal/config"
	appcontainer "github.com/VladimirovArtem/template/internal/container"
	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/gateway/push"
	transport "github.com/VladimirovArtem/template/internal/http"
	"github.com/VladimirovArtem/template/internal/http/health"
	"github.com/VladimirovArtem/template/internal/http/middleware"
	positioncreatehandler "github.com/VladimirovArtem/template/internal/http/position/create"
	positionlisthandler "github.com/VladimirovArtem/template/internal/http/position/list"
	"github.com/VladimirovArtem/template/internal/http/response"
	createhandler "github.com/VladimirovArtem/template/internal/http/trip/create"
	finishhandler "github.com/VladimirovArtem/template/internal/http/trip/finish"
	gethandler "github.com/VladimirovArtem/template/internal/http/trip/get"
	"github.com/VladimirovArtem/template/internal/infra/postgres/transaction"
	"github.com/VladimirovArtem/template/internal/infra/telemetry"
	"github.com/VladimirovArtem/template/internal/model"
	repository "github.com/VladimirovArtem/template/internal/repository/trip"
	positioncreate "github.com/VladimirovArtem/template/internal/usecase/position/create"
	positionlist "github.com/VladimirovArtem/template/internal/usecase/position/list"
	"github.com/VladimirovArtem/template/internal/usecase/trip/create"
	"github.com/VladimirovArtem/template/internal/usecase/trip/finish"
	"github.com/VladimirovArtem/template/internal/usecase/trip/get"
	"github.com/VladimirovArtem/template/internal/worker/positions"
)

type runtimeTicker struct {
	ticks   chan time.Time
	stopped chan struct{}
	once    sync.Once
}

func (t *runtimeTicker) Ticks() <-chan time.Time { return t.ticks }
func (t *runtimeTicker) Stop()                   { t.once.Do(func() { close(t.stopped) }) }

type runtimePositionEvents struct {
	*telemetry.Positions
	scanned chan int
}

func (e *runtimePositionEvents) RowsRead(ctx context.Context, count int) {
	e.Positions.RowsRead(ctx, count)
	e.scanned <- count
}

type runtimePush struct {
	*httptest.Server
	entered chan context.Context
	release chan struct{}
	calls   atomic.Int64
}

func newRuntimePush(t *testing.T, tripID, driverID uuid.UUID) *runtimePush {
	t.Helper()
	stub := &runtimePush{entered: make(chan context.Context, 1), release: make(chan struct{})}
	stub.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Recipient uuid.UUID         `json:"recipient_id"`
			Data      map[string]string `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		if body.Recipient != driverID || body.Data["trip_id"] != tripID.String() {
			t.Error("canceled trip or wrong driver was pushed")
		}
		stub.calls.Add(1)
		stub.entered <- r.Context()
		select {
		case <-stub.release:
			w.WriteHeader(http.StatusAccepted)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(stub.Close)
	return stub
}

func TestPositionsRuntimeWithPostgres(t *testing.T) {
	ctx := testContext(t)
	at := time.Now().UTC().Truncate(time.Microsecond)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	exporter := tracetest.NewInMemoryExporter()
	traces := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	reader := sdkmetric.NewManualReader()
	meters := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		if err := traces.Shutdown(cleanup); err != nil {
			t.Error(err)
		}
		if err := meters.Shutdown(cleanup); err != nil {
			t.Error(err)
		}
	})
	dbTracer, err := telemetry.NewPostgresTracer(traces, meters)
	if err != nil {
		t.Fatal(err)
	}
	dbConfig := testPool.Config().Copy()
	dbConfig.ConnConfig.Tracer = dbTracer
	pool, err := pgxpool.NewWithConfig(ctx, dbConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	repo := repository.New(pool, queryTimeout)
	tx := transaction.New(pool, queryTimeout, logger)
	primerInput, targetInput := newInput(t), newInput(t)
	primer, err := create.New(repo, tx, uuid.New, func() time.Time { return at.Add(-3 * time.Hour) }, testEvents).Execute(ctx, primerInput)
	if err != nil {
		t.Fatal(err)
	}
	target, err := create.New(repo, tx, uuid.New, func() time.Time { return at.Add(-2 * time.Hour) }, testEvents).Execute(ctx, targetInput)
	if err != nil {
		t.Fatal(err)
	}
	pushServer := newRuntimePush(t, primer.ID, primer.DriverID)
	gateway, err := push.New(pushServer.URL, queryTimeout, traces, meters)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gateway.CloseIdleConnections)
	events, err := telemetry.NewPositions(meters)
	if err != nil {
		t.Fatal(err)
	}
	signals := &runtimePositionEvents{Positions: events, scanned: make(chan int, 1)}
	requests, err := positions.New(gateway, repo, 1, 10, queryTimeout, logger, events, traces.Tracer("integration"))
	if err != nil {
		t.Fatal(err)
	}
	if err := telemetry.RegisterPositionState(meters, requests); err != nil {
		t.Fatal(err)
	}
	ticker := &runtimeTicker{ticks: make(chan time.Time, 1), stopped: make(chan struct{})}
	worker, err := positions.NewWorker(repo, requests, positions.ScanConfig{Interval: time.Second, StaleAfter: time.Hour, BatchSize: 100}, func(time.Duration) positions.Ticker { return ticker }, func() time.Time { return at }, signals, traces.Tracer("integration"), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		worker.Abort()
		if err := worker.Shutdown(cleanup); err != nil {
			t.Error(err)
		}
	})
	writer := response.New(logger)
	instrumentation, err := middleware.New(logger, writer, traces, meters)
	if err != nil {
		t.Fatal(err)
	}
	router, err := transport.NewRouter(createhandler.New(create.New(repo, tx, uuid.New, time.Now, testEvents), writer), gethandler.New(get.New(repo), writer),
		finishhandler.New(finish.New(repo, tx, time.Now, testEvents), writer), positioncreatehandler.New(positioncreate.New(repo, tx, func() time.Time { return at }, requests, events), writer),
		positionlisthandler.New(positionlist.New(repo), writer), health.New(pool, queryTimeout, writer), writer, instrumentation)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	ticker.ticks <- at
	requestCtx := awaitRuntimeScan(t, ctx, signals, pushServer)
	saveRuntimePosition(t, ctx, server, repo, target.ID, at)
	if pending, active := requests.Snapshot(); pending != 0 || active != 1 {
		t.Fatalf("after save: pending=%d active=%d", pending, active)
	}
	if err := server.Config.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- worker.Shutdown(ctx) }()
	select {
	case <-ticker.stopped:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if requestCtx.Err() != nil {
		t.Fatal("stopping scanner canceled a draining Push")
	}
	close(pushServer.release)
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if pushServer.calls.Load() != 1 {
		t.Fatalf("pushes=%d", pushServer.calls.Load())
	}
	assertRuntimeSignals(t, ctx, reader, exporter)
	t.Log("real PostgreSQL → scanner → priority queue → HTTP Push: 2 queued, 1 saved+cancelled, 1 actual Push; scan SQL child and worker span link verified")
}

func TestPoolSkipsTripsChangedAfterScan(t *testing.T) {
	ctx := testContext(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	old := staleFixture{status: trip.StatusActive, startedAt: now.Add(-2 * time.Hour)}
	insertStaleFixtures(t, ctx, []staleFixture{old, old})
	repo := repository.New(testPool, queryTimeout)
	batch, err := repo.ListStaleTrips(ctx, now, time.Hour, 10, nil)
	if err != nil || len(batch) != 2 {
		t.Fatalf("selected %d trips, want 2: %v", len(batch), err)
	}

	var posts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		posts.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)
	traces, meters := tracenoop.NewTracerProvider(), metricnoop.NewMeterProvider()
	gateway, err := push.New(server.URL, queryTimeout, traces, meters)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gateway.CloseIdleConnections)
	events, err := telemetry.NewPositions(meters)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool, err := positions.New(gateway, repo, 1, len(batch), queryTimeout, logger, events, traces.Tracer("test"))
	if err != nil {
		t.Fatal(err)
	}
	tx := transaction.New(testPool, queryTimeout, logger)
	save := positioncreate.New(repo, tx, time.Now, pool, events)
	if _, err := save.Execute(ctx, model.CreatePositionInput{TripID: batch[0].ID, RecordedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := finish.New(repo, tx, time.Now, testEvents).Execute(ctx, batch[1].ID); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range batch {
		if result := pool.Put(ctx, candidate); result.Status != positions.Queued {
			t.Fatalf("queue result=%s", result.Status)
		}
	}
	if err := pool.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 0 {
		t.Fatalf("sent %d pushes after saving a fresh point and finishing a trip", posts.Load())
	}
}

func awaitRuntimeScan(t *testing.T, ctx context.Context, signals *runtimePositionEvents, pushServer *runtimePush) context.Context {
	t.Helper()
	select {
	case rows := <-signals.scanned:
		if rows != 2 {
			t.Fatalf("scanned rows=%d", rows)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case requestCtx := <-pushServer.entered:
		return requestCtx
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	return nil
}

func saveRuntimePosition(t *testing.T, ctx context.Context, server *httptest.Server, repo *repository.Repository, tripID uuid.UUID, at time.Time) {
	t.Helper()
	client := server.Client()
	client.Timeout = testTimeout
	t.Cleanup(client.CloseIdleConnections)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/api/v1/trips/"+tripID.String()+"/positions", strings.NewReader(positionJSON(at)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	result, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var point api.TripPosition
	if err := json.NewDecoder(result.Body).Decode(&point); err != nil {
		t.Error(err)
	}
	_ = result.Body.Close()
	if result.StatusCode != http.StatusCreated || point.TripId != tripID {
		t.Fatalf("position status=%d point=%+v", result.StatusCode, point)
	}
	found, err := repo.Get(ctx, tripID)
	if err != nil || found.LastPositionAt == nil || !found.LastPositionAt.Equal(at) {
		t.Fatalf("persisted coordinate: %+v, %v", found, err)
	}
}

func assertRuntimeSignals(t *testing.T, ctx context.Context, reader *sdkmetric.ManualReader, exporter *tracetest.InMemoryExporter) {
	t.Helper()
	metrics := collectMetrics(t, ctx, reader)
	if runtimeCounter(metrics, "tripgo.positions.saved", "") != 1 || runtimeCounter(metrics, "tripgo.position_requests", "cancelled") != 1 || runtimeCounter(metrics, "tripgo.position_requests", "success") != 1 || runtimeCounter(metrics, "tripgo.stale_checks", "queued") != 2 {
		t.Fatal("runtime counters mismatch")
	}
	for _, name := range []string{"tripgo.position_requests.queue.size", "tripgo.position_requests.workers.active"} {
		if metrics[name].Data.(metricdata.Gauge[int64]).DataPoints[0].Value != 0 {
			t.Fatal("runtime gauge did not return to zero")
		}
	}
	var checkID string
	var linked, sqlChildren int
	for _, span := range exporter.GetSpans() {
		if span.Name == "positions.check" {
			checkID = span.SpanContext.SpanID().String()
		}
	}
	for _, span := range exporter.GetSpans() {
		if span.Name == "positions.request" && len(span.Links) == 1 && span.Links[0].SpanContext.SpanID().String() == checkID && !span.Parent.IsValid() {
			linked++
		}
		if span.Parent.SpanID().String() == checkID {
			sqlChildren++
		}
	}
	if linked != 1 || sqlChildren != 1 {
		t.Fatalf("scan trace: linked workers=%d SQL children=%d", linked, sqlChildren)
	}
}

func runtimeCounter(metrics map[string]metricdata.Metrics, name, result string) int64 {
	item, ok := metrics[name]
	if !ok {
		return 0
	}
	var total int64
	for _, point := range item.Data.(metricdata.Sum[int64]).DataPoints {
		value, _ := point.Attributes.Value("result")
		if value.AsString() == result {
			total += point.Value
		}
	}
	return total
}

func TestContainerInitializationAndOccupiedPortWithPostgres(t *testing.T) {
	ctx := testContext(t)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()
	pushServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("background started before HTTP bind succeeded") }))
	defer pushServer.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	dbConfig := testPool.Config().Copy()
	dbConfig.ConnConfig.ConnectTimeout = time.Second
	cfg := config.Config{
		HTTPAddr: listener.Addr().String(), ShutdownTimeout: queryTimeout, ReadTimeout: queryTimeout, ReadHeaderTimeout: time.Second, WriteTimeout: 2 * queryTimeout, IdleTimeout: queryTimeout, QueryTimeout: queryTimeout, Pool: dbConfig,
		Telemetry: config.Telemetry{ServiceName: "trip-service", Endpoint: collector.URL, Protocol: "http/protobuf", ResourceAttributes: "service.namespace=tripgo,deployment.environment=local", Sampler: "always_on"},
		Positions: config.Positions{CheckInterval: time.Hour, StaleAfter: time.Minute, RequestTimeout: time.Second, Workers: 1, QueueSize: 10, ScanBatchSize: 100, PushURL: pushServer.URL},
	}
	invalid := cfg
	invalid.Positions.ScanBatchSize = 0
	if service, err := appcontainer.New(ctx, invalid, slog.New(slog.NewJSONHandler(io.Discard, nil))); err == nil || service != nil {
		t.Fatalf("invalid scanner initialization: service=%v error=%v", service, err)
	}
	service, err := appcontainer.New(ctx, cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Run(ctx); err == nil {
		t.Fatal("occupied port accepted")
	}
	if err := testPool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
}
