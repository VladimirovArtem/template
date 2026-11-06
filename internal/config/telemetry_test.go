package config

import (
	"net/url"
	"testing"
)

func TestLoadTelemetry(t *testing.T) {
	const endpointEnv = "OTEL_EXPORTER_OTLP_ENDPOINT"
	const protocolEnv = "OTEL_EXPORTER_OTLP_PROTOCOL"
	credentials := (&url.URL{Scheme: "http", Host: "collector.invalid", User: url.UserPassword("test-user", t.Name())}).String()
	for _, tt := range []struct {
		name    string
		key     string
		value   string
		invalid bool
	}{
		{name: "HTTP"},
		{name: "gRPC", key: protocolEnv, value: "grpc"},
		{name: "missing service", key: "OTEL_SERVICE_NAME", invalid: true},
		{name: "missing endpoint", key: endpointEnv, invalid: true},
		{name: "endpoint credentials", key: endpointEnv, value: credentials, invalid: true},
		{name: "unsupported protocol", key: protocolEnv, value: "json", invalid: true},
		{name: "missing resource", key: "OTEL_RESOURCE_ATTRIBUTES", invalid: true},
		{name: "missing sampler", key: "OTEL_TRACES_SAMPLER", invalid: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for key, value := range map[string]string{
				"OTEL_SERVICE_NAME":        "trip-service",
				endpointEnv:                "http://collector.invalid",
				protocolEnv:                "http/protobuf",
				"OTEL_RESOURCE_ATTRIBUTES": "service.namespace=tripgo,deployment.environment=local",
				"OTEL_TRACES_SAMPLER":      "parentbased_always_on",
			} {
				t.Setenv(key, value)
			}
			if tt.key != "" {
				t.Setenv(tt.key, tt.value)
			}
			_, err := loadTelemetry()
			if (err != nil) != tt.invalid {
				t.Fatalf("loadTelemetry() = %v", err)
			}
		})
	}
}
