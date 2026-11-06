package telemetry_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/VladimirovArtem/template/internal/http/domainerror"
	"github.com/VladimirovArtem/template/internal/http/middleware"
	"github.com/VladimirovArtem/template/internal/http/response"
	"github.com/VladimirovArtem/template/internal/logging"
)

func TestHTTPSignals(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		status  int
		panic   bool
		unknown bool
	}{
		{name: "success", status: 200},
		{name: "client error", status: 400},
		{name: "panic", status: 500, panic: true},
		{name: "PostgreSQL failure", status: 500},
		{name: "unknown path and method", status: 405, unknown: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			traces, meters, reader, exporter := memoryTelemetry(t)
			var logs bytes.Buffer
			logger := slog.New(logging.Handler{Handler: slog.NewJSONHandler(&logs, nil)}).With("service", testServiceName)
			writer := response.New(logger)
			instrumentation, err := middleware.New(logger, writer, traces, meters)
			if err != nil {
				t.Fatal(err)
			}
			router := chi.NewRouter()
			router.Get("/api/v1/trips/{tripId}", func(w http.ResponseWriter, r *http.Request) {
				var inFlight metricdata.ResourceMetrics
				if err := reader.Collect(r.Context(), &inFlight); err != nil {
					t.Fatal(err)
				}
				active := findMetric(t, inFlight, "http.server.active_requests").Data.(metricdata.Sum[int64])
				if len(active.DataPoints) != 1 || active.DataPoints[0].Value != 1 {
					t.Fatal("active request was not observed")
				}
				if tt.panic {
					panic("password=secret")
				}
				if tt.status == http.StatusBadRequest {
					writer.InvalidRequest(w, r)
					return
				}
				if tt.status == http.StatusInternalServerError {
					writer.Error(w, r, "get trip", fmt.Errorf("get trip: %w", &pgconn.PgError{Code: "42501", Message: "password=secret"}))
					return
				}
				writer.JSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
			})
			router.NotFound(func(w http.ResponseWriter, r *http.Request) {
				writer.Problem(w, r, 404, domainerror.New(404, "not_found", "Not Found", "Route was not found"))
			})
			router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
				writer.Problem(w, r, 405, domainerror.New(405, "method_not_allowed", "Method Not Allowed", "Method is not allowed for this route"))
			})
			id := uuid.NewString()
			method, path, route := http.MethodGet, "/api/v1/trips/"+id, "/api/v1/trips/{tripId}"
			if tt.unknown {
				method, path, route = id, "/unknown/"+id, "/unmatched"
			}
			request := httptest.NewRequest(method, path, nil)
			request.Header.Set("traceparent", "00-11111111111111111111111111111111-2222222222222222-01")
			recorder := httptest.NewRecorder()
			instrumentation.Wrap(router, router).ServeHTTP(recorder, request)
			if recorder.Code != tt.status {
				t.Fatalf("status=%d, body=%s", recorder.Code, recorder.Body)
			}
			if strings.Count(logs.String(), "\n") != 1 || strings.Contains(logs.String(), id) || strings.Contains(logs.String(), "password=secret") {
				t.Fatalf("unsafe or duplicated log: %s", &logs)
			}
			var record map[string]any
			if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			level := "INFO"
			if tt.status >= 500 {
				level = "ERROR"
			}
			if record["level"] != level || record["route"] != route || record["status"] != float64(tt.status) ||
				record["service"] != testServiceName || record["duration_ms"] == nil {
				t.Fatalf("log fields: %v", record)
			}
			if tt.panic && record["stack"] == nil {
				t.Fatal("panic stack missing")
			}
			if tt.status == http.StatusInternalServerError && !tt.panic {
				if record["sqlstate"] != "42501" || record["operation"] != "get trip" || record["cause"] == nil {
					t.Fatalf("PostgreSQL diagnosis missing: %v", record)
				}
			}
			checkHTTPTrace(t, exporter.GetSpans(), record, method, route, tt.status, tt.panic, tt.unknown)
			var output metricdata.ResourceMetrics
			if err := reader.Collect(t.Context(), &output); err != nil {
				t.Fatal(err)
			}
			checkHTTPMetrics(t, output, route, id, tt.status)
		})
	}
}

func checkHTTPTrace(t *testing.T, spans tracetest.SpanStubs, record map[string]any, method, route string, statusCode int, panicked, unknown bool) {
	t.Helper()

	if len(spans) != 1 || spans[0].SpanKind != trace.SpanKindServer {
		t.Fatalf("server spans=%d", len(spans))
	}
	span := spans[0]
	expectedMethod := method
	if unknown {
		expectedMethod = "_OTHER"
	}
	if span.Name != expectedMethod+" "+route || record["trace_id"] != span.SpanContext.TraceID().String() ||
		record["span_id"] != span.SpanContext.SpanID().String() || span.SpanContext.TraceID().String() != "11111111111111111111111111111111" ||
		span.Parent.SpanID().String() != "2222222222222222" {
		t.Fatalf("uncorrelated span: %+v", span)
	}
	for _, key := range []attribute.Key{"service.name", "service.version", "service.namespace", "deployment.environment"} {
		value, exists := span.Resource.Set().Value(key)
		if !exists || value.AsString() == "" {
			t.Fatalf("SDK span resource %s missing", key)
		}
	}
	attrs := attribute.NewSet(span.Attributes...)
	spanRoute, _ := attrs.Value("http.route")
	status, _ := attrs.Value("http.response.status_code")
	if spanRoute.AsString() != route || status.AsInt64() != int64(statusCode) {
		t.Fatalf("span attributes: %v", attrs.ToSlice())
	}
	if panicked {
		kind, _ := attrs.Value("error.type")
		if span.Status.Code != codes.Error || kind.AsString() != "panic" {
			t.Fatalf("panic span: %+v", span)
		}
	}
}

func checkHTTPMetrics(t *testing.T, output metricdata.ResourceMetrics, route, id string, statusCode int) {
	t.Helper()
	activeMetric := findMetric(t, output, "http.server.active_requests")
	active := activeMetric.Data.(metricdata.Sum[int64])
	if activeMetric.Unit != "{request}" || active.IsMonotonic || len(active.DataPoints) != 1 || active.DataPoints[0].Value != 0 || active.DataPoints[0].Attributes.Len() != 2 {
		t.Fatalf("active request did not return to zero: %+v", activeMetric)
	}
	durationMetric := findMetric(t, output, "http.server.request.duration")
	duration := durationMetric.Data.(metricdata.Histogram[float64])
	if durationMetric.Unit != "s" || len(duration.DataPoints) != 1 || duration.DataPoints[0].Count != 1 {
		t.Fatalf("duration recorded more than once: %+v", durationMetric)
	}
	point := duration.DataPoints[0]
	metricRoute, _ := point.Attributes.Value("http.route")
	metricStatus, _ := point.Attributes.Value("http.response.status_code")
	if metricRoute.AsString() != route || metricStatus.AsInt64() != int64(statusCode) || strings.Contains(point.Attributes.Encoded(attribute.DefaultEncoder()), id) {
		t.Fatalf("duration attributes: %v", point.Attributes.ToSlice())
	}
}
