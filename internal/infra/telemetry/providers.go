package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"runtime/debug"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/VladimirovArtem/template/internal/config"
)

type Providers struct {
	Traces  *sdktrace.TracerProvider
	Metrics *sdkmetric.MeterProvider
}

func New(ctx context.Context, cfg config.Telemetry) (*Providers, error) {
	res, err := Resource(cfg)
	if err != nil {
		return nil, err
	}
	sampler, err := sampler(cfg.Sampler)
	if err != nil {
		return nil, err
	}
	var spans sdktrace.SpanExporter
	var metrics sdkmetric.Exporter
	if cfg.Protocol == "grpc" {
		spans, err = otlptracegrpc.New(ctx, otlptracegrpc.WithEndpointURL(cfg.Endpoint))
		if err == nil {
			metrics, err = otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithEndpointURL(cfg.Endpoint))
		}
	} else {
		endpoint, parseErr := url.Parse(cfg.Endpoint)
		if parseErr != nil {
			return nil, fmt.Errorf("parse OTLP HTTP endpoint: %w", parseErr)
		}
		tracesURL := endpoint.JoinPath("v1", "traces").String()
		metricsURL := endpoint.JoinPath("v1", "metrics").String()
		spans, err = otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(tracesURL))
		if err == nil {
			metrics, err = otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(metricsURL))
		}
	}
	if err != nil {
		if spans != nil {
			err = errors.Join(err, spans.Shutdown(ctx))
		}
		return nil, fmt.Errorf("initialize OTLP exporters: %w", err)
	}
	return &Providers{
		Traces: sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithSampler(sampler), sdktrace.WithBatcher(spans)),
		Metrics: sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(res), sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metrics)),
			sdkmetric.WithView(sdkmetric.NewView(sdkmetric.Instrument{Name: "http.server.*"}, sdkmetric.Stream{
				AttributeFilter: attribute.NewAllowKeysFilter("http.request.method", "http.route", "http.response.status_code"),
			})),
		),
	}, nil
}

func (p *Providers) Shutdown(ctx context.Context) error {
	return errors.Join(p.Traces.Shutdown(ctx), p.Metrics.Shutdown(ctx))
}

func Resource(cfg config.Telemetry) (*resource.Resource, error) {
	version := "devel"
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" {
			version = info.Main.Version
		}
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				version = setting.Value
			}
		}
	}
	attrs := []attribute.KeyValue{attribute.String("service.version", version)}
	for _, item := range strings.Split(cfg.ResourceAttributes, ",") {
		key, value, valid := strings.Cut(item, "=")
		if !valid || strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return nil, errors.New("OTEL_RESOURCE_ATTRIBUTES: required comma-separated key=value attributes")
		}
		attrs = append(attrs, attribute.String(strings.TrimSpace(key), strings.TrimSpace(value)))
	}
	attrs = append(attrs, attribute.String("service.name", cfg.ServiceName))
	res := resource.NewSchemaless(attrs...)
	for _, key := range []attribute.Key{"service.name", "service.version", "service.namespace", "deployment.environment"} {
		value, ok := res.Set().Value(key)
		if !ok || value.AsString() == "" {
			return nil, fmt.Errorf("telemetry resource: required %s", key)
		}
	}
	return res, nil
}

func sampler(name string) (sdktrace.Sampler, error) {
	switch name {
	case "always_on":
		return sdktrace.AlwaysSample(), nil
	case "always_off":
		return sdktrace.NeverSample(), nil
	case "parentbased_always_on":
		return sdktrace.ParentBased(sdktrace.AlwaysSample()), nil
	case "parentbased_always_off":
		return sdktrace.ParentBased(sdktrace.NeverSample()), nil
	default:
		return nil, errors.New("OTEL_TRACES_SAMPLER: required always_on, always_off, parentbased_always_on or parentbased_always_off")
	}
}
