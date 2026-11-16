package telemetry_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/VladimirovArtem/template/internal/config"
	"github.com/VladimirovArtem/template/internal/infra/telemetry"
)

func TestOTLPHTTPExportPaths(t *testing.T) {
	t.Parallel()
	const exportTimeout = 5 * time.Second
	for _, tt := range []struct {
		name        string
		prefix      string
		tracesPath  string
		metricsPath string
	}{
		{name: "base URL", tracesPath: "/v1/traces", metricsPath: "/v1/metrics"},
		{name: "path prefix", prefix: "/otel/", tracesPath: "/otel/v1/traces", metricsPath: "/otel/v1/metrics"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requests = append(requests, r.Method+" "+r.URL.Path)
				mu.Unlock()
				if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-protobuf" {
					t.Errorf("unexpected OTLP request: %s %s, Content-Type=%s", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
				}
				size, err := io.Copy(io.Discard, r.Body)
				if err != nil || size == 0 {
					t.Errorf("OTLP payload: size=%d, error=%v", size, err)
				}
				switch r.URL.Path {
				case tt.tracesPath, tt.metricsPath:
					w.Header().Set("Content-Type", "application/x-protobuf")
					w.WriteHeader(http.StatusOK)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			providers, err := telemetry.New(t.Context(), config.Telemetry{
				ServiceName:        testServiceName,
				Endpoint:           server.URL + tt.prefix,
				Protocol:           "http/protobuf",
				ResourceAttributes: "service.namespace=tripgo,deployment.environment=test",
				Sampler:            "always_on",
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), exportTimeout)
				defer cancel()
				if err := providers.Shutdown(ctx); err != nil {
					t.Errorf("shutdown telemetry: %v", err)
				}
			})
			_, span := providers.Traces.Tracer("otlp-http-test").Start(t.Context(), "export test")
			span.End()
			counter, err := providers.Metrics.Meter("otlp-http-test").Int64Counter("otlp.test.counter")
			if err != nil {
				t.Fatal(err)
			}
			counter.Add(t.Context(), 1)
			ctx, cancel := context.WithTimeout(t.Context(), exportTimeout)
			defer cancel()
			traceErr := providers.Traces.ForceFlush(ctx)
			metricErr := providers.Metrics.ForceFlush(ctx)
			mu.Lock()
			received := slices.Clone(requests)
			mu.Unlock()
			slices.Sort(received)
			want := []string{http.MethodPost + " " + tt.tracesPath, http.MethodPost + " " + tt.metricsPath}
			slices.Sort(want)
			t.Logf("received HTTP requests: %q", received)
			if traceErr != nil {
				t.Errorf("flush traces: %v", traceErr)
			}
			if metricErr != nil {
				t.Errorf("flush metrics: %v", metricErr)
			}
			if !slices.Equal(received, want) {
				t.Errorf("HTTP requests = %q, want %q", received, want)
			}
		})
	}
}
