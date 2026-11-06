package telemetry_test

import (
	"context"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/VladimirovArtem/template/internal/config"
	"github.com/VladimirovArtem/template/internal/infra/telemetry"
)

const testServiceName = "trip-service"

func memoryTelemetry(t *testing.T) (*sdktrace.TracerProvider, *sdkmetric.MeterProvider, *sdkmetric.ManualReader, *tracetest.InMemoryExporter) {
	t.Helper()
	res, err := telemetry.Resource(config.Telemetry{ServiceName: testServiceName, ResourceAttributes: "service.namespace=tripgo,deployment.environment=local"})
	if err != nil {
		t.Fatal(err)
	}
	exporter := tracetest.NewInMemoryExporter()
	traces := sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithSyncer(exporter), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	reader := sdkmetric.NewManualReader()
	meters := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := traces.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		if err := meters.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	return traces, meters, reader, exporter
}

func findMetric(t *testing.T, data metricdata.ResourceMetrics, name string) metricdata.Metrics {
	t.Helper()
	for _, scope := range data.ScopeMetrics {
		for _, item := range scope.Metrics {
			if item.Name == name {
				return item
			}
		}
	}
	t.Fatalf("metric %s missing from SDK output", name)
	return metricdata.Metrics{}
}
