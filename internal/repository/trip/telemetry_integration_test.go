//go:build integration

package trip_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/VladimirovArtem/template/internal/config"
	"github.com/VladimirovArtem/template/internal/domain/trip"
	transport "github.com/VladimirovArtem/template/internal/http"
	"github.com/VladimirovArtem/template/internal/http/health"
	"github.com/VladimirovArtem/template/internal/http/middleware"
	"github.com/VladimirovArtem/template/internal/http/response"
	createhandler "github.com/VladimirovArtem/template/internal/http/trip/create"
	finishhandler "github.com/VladimirovArtem/template/internal/http/trip/finish"
	gethandler "github.com/VladimirovArtem/template/internal/http/trip/get"
	"github.com/VladimirovArtem/template/internal/infra/postgres/transaction"
	"github.com/VladimirovArtem/template/internal/infra/telemetry"
	"github.com/VladimirovArtem/template/internal/logging"
	repository "github.com/VladimirovArtem/template/internal/repository/trip"
	"github.com/VladimirovArtem/template/internal/usecase/trip/create"
	"github.com/VladimirovArtem/template/internal/usecase/trip/finish"
	"github.com/VladimirovArtem/template/internal/usecase/trip/get"
)

func TestHTTPPostgresTelemetry(t *testing.T) {
	ctx := testContext(t)
	input := newInput(t)
	res, err := telemetry.Resource(config.Telemetry{ServiceName: "trip-service", ResourceAttributes: "service.namespace=tripgo,deployment.environment=local"})
	if err != nil {
		t.Fatal(err)
	}
	exporter := tracetest.NewInMemoryExporter()
	traces := sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithSyncer(exporter), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	reader := sdkmetric.NewManualReader()
	meters := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), sdkmetric.WithReader(reader))
	dbTracer, err := telemetry.NewPostgresTracer(traces, meters)
	if err != nil {
		t.Fatal(err)
	}
	poolConfig := testPool.Config().Copy()
	poolConfig.ConnConfig.Tracer = dbTracer
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		if err := traces.Shutdown(cleanupCtx); err != nil {
			t.Error(err)
		}
		if err := meters.Shutdown(cleanupCtx); err != nil {
			t.Error(err)
		}
		pool.Close()
	})
	var logs bytes.Buffer
	logger := slog.New(logging.Handler{Handler: slog.NewJSONHandler(&logs, nil)}).With("service", "trip-service")
	repo := repository.New(pool, queryTimeout)
	tx := transaction.New(pool, queryTimeout, logger)
	events, err := telemetry.NewTrips(meters, logger)
	if err != nil {
		t.Fatal(err)
	}
	creator := create.New(repo, tx, uuid.New, time.Now, events)
	finisher := finish.New(repo, tx, time.Now, events)
	created, err := creator.Execute(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	logs.Reset()
	writer := response.New(logger)
	instrumentation, err := middleware.New(logger, writer, traces, meters)
	if err != nil {
		t.Fatal(err)
	}
	router, err := transport.NewRouter(createhandler.New(creator, writer), gethandler.New(get.New(repo), writer),
		finishhandler.New(finisher, writer), health.New(pool, queryTimeout, writer), writer, instrumentation)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/api/v1/trips/"+created.ID.String(), nil).WithContext(ctx)
	request.Header.Set("traceparent", "00-33333333333333333333333333333333-4444444444444444-01")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("GET: %d %s", recorder.Code, recorder.Body)
	}
	requestLog := logs.String()
	var record map[string]any
	if err := json.Unmarshal([]byte(requestLog), &record); err != nil {
		t.Fatal(err)
	}
	server, database := checkTraceLink(t, exporter.GetSpans(), record)
	metrics := collectMetrics(t, ctx, reader)
	checkDatabaseDuration(t, metrics, created.ID.String())
	if err := telemetry.RegisterActiveTrips(meters, repo, queryTimeout); err != nil {
		t.Fatal(err)
	}
	metrics = collectMetrics(t, ctx, reader)
	if gauge := metrics["tripgo.trips.active"].Data.(metricdata.Gauge[int64]); len(gauge.DataPoints) != 1 || gauge.DataPoints[0].Value != 1 {
		t.Fatalf("active trip gauge before finish: %+v", gauge)
	}
	t.Logf("telemetry log: %s", strings.TrimSpace(requestLog))
	t.Logf("telemetry spans: HTTP=%s name=%q, DB=%s parent=%s operation=SELECT collection=trips trace_id=%s",
		server.SpanContext.SpanID(), server.Name, database.SpanContext.SpanID(), database.Parent.SpanID(), server.SpanContext.TraceID())
	errorCtx, errorSpan := traces.Tracer("integration").Start(ctx, "duplicate trip")
	_, err = repo.Insert(errorCtx, trip.Trip{
		ID: uuid.New(), UserID: input.UserID, DriverID: input.DriverID,
		StartPoint: input.StartPoint, EndPoint: input.EndPoint, Price: input.Price,
		Status: trip.StatusActive, StartedAt: time.Now().UTC(),
	})
	errorSpan.End()
	if !errors.Is(err, trip.ErrDriverBusy) {
		t.Fatalf("duplicate trip: %v", err)
	}
	checkFailedDatabaseSpan(t, exporter.GetSpans(), input.DriverID.String())
	if _, err := finisher.Execute(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	metrics = collectMetrics(t, ctx, reader)
	for _, name := range []string{"http.server.request.duration", "http.server.active_requests", "db.client.operation.duration", "tripgo.trips.created", "tripgo.trips.completed", "tripgo.trips.active"} {
		if _, exists := metrics[name]; !exists {
			t.Fatalf("missing metric %s", name)
		}
	}
	if metrics["tripgo.trips.created"].Data.(metricdata.Sum[int64]).DataPoints[0].Value != 1 ||
		metrics["tripgo.trips.completed"].Data.(metricdata.Sum[int64]).DataPoints[0].Value != 1 ||
		metrics["tripgo.trips.active"].Data.(metricdata.Gauge[int64]).DataPoints[0].Value != 0 {
		t.Fatal("trip metrics do not match committed database state")
	}
	var errorCount uint64
	for _, point := range metrics["db.client.operation.duration"].Data.(metricdata.Histogram[float64]).DataPoints {
		kind, _ := point.Attributes.Value("error.type")
		if kind.AsString() == "23505" {
			errorCount += point.Count
		}
	}
	if errorCount != 1 {
		t.Fatalf("database error metric count=%d", errorCount)
	}
	names := make([]string, 0, len(metrics))
	for name := range metrics {
		names = append(names, name)
	}
	sort.Strings(names)
	t.Logf("SDK metric names: %s", strings.Join(names, ", "))
}

func collectMetrics(t *testing.T, ctx context.Context, reader *sdkmetric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()
	var output metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &output); err != nil {
		t.Fatal(err)
	}
	metrics := make(map[string]metricdata.Metrics)
	for _, scope := range output.ScopeMetrics {
		for _, item := range scope.Metrics {
			if _, duplicate := metrics[item.Name]; duplicate {
				t.Fatalf("duplicate instrument %s", item.Name)
			}
			metrics[item.Name] = item
		}
	}
	return metrics
}
