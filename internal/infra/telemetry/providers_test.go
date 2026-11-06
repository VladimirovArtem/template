package telemetry_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/attribute"

	"github.com/VladimirovArtem/template/internal/config"
	"github.com/VladimirovArtem/template/internal/http/middleware"
	"github.com/VladimirovArtem/template/internal/http/response"
	"github.com/VladimirovArtem/template/internal/infra/telemetry"
)

func TestResource(t *testing.T) {
	t.Parallel()
	cfg := config.Telemetry{ServiceName: testServiceName, ResourceAttributes: "service.namespace=tripgo,deployment.environment=local,service.version=test-build"}
	res, err := telemetry.Resource(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[attribute.Key]string{"service.name": testServiceName, "service.namespace": "tripgo", "deployment.environment": "local", "service.version": "test-build"} {
		got, ok := res.Set().Value(key)
		if !ok || got.AsString() != want {
			t.Fatalf("resource %s=%v", key, got)
		}
	}
}

func TestUnavailableCollector(t *testing.T) {
	t.Parallel()
	for _, protocol := range []string{"http/protobuf", "grpc"} {
		t.Run(protocol, func(t *testing.T) {
			t.Parallel()
			endpoint := httptest.NewServer(http.NotFoundHandler())
			endpoint.Close()
			cfg := config.Telemetry{
				ServiceName: testServiceName, Endpoint: endpoint.URL, Protocol: protocol,
				ResourceAttributes: "service.namespace=tripgo,deployment.environment=local", Sampler: "parentbased_always_on",
			}
			providers, err := telemetry.New(t.Context(), cfg)
			if err != nil {
				t.Fatalf("offline collector blocked initialization: %v", err)
			}
			logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
			writer := response.New(logger)
			instrumentation, err := middleware.New(logger, writer, providers.Traces, providers.Metrics)
			if err != nil {
				t.Fatal(err)
			}
			router := chi.NewRouter()
			router.Get("/ready", func(w http.ResponseWriter, r *http.Request) {
				writer.JSON(w, r, 200, map[string]string{"status": "ok"})
			})
			recorder := httptest.NewRecorder()
			instrumentation.Wrap(router, router).ServeHTTP(recorder, httptest.NewRequest("GET", "/ready", nil))
			if recorder.Code != 200 {
				t.Fatalf("collector affected ready: %d", recorder.Code)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			// A spent shared shutdown budget must not wait for an unavailable collector.
			if err := providers.Shutdown(ctx); err != nil && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}
