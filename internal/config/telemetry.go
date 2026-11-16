package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

type Telemetry struct {
	ServiceName        string
	Endpoint           string
	Protocol           string
	ResourceAttributes string
	Sampler            string
}

func loadTelemetry() (Telemetry, error) {
	var cfg Telemetry
	for _, field := range []struct {
		name string
		dest *string
	}{
		{"OTEL_SERVICE_NAME", &cfg.ServiceName},
		{"OTEL_EXPORTER_OTLP_ENDPOINT", &cfg.Endpoint},
		{"OTEL_EXPORTER_OTLP_PROTOCOL", &cfg.Protocol},
		{"OTEL_RESOURCE_ATTRIBUTES", &cfg.ResourceAttributes},
		{"OTEL_TRACES_SAMPLER", &cfg.Sampler},
	} {
		*field.dest = strings.TrimSpace(os.Getenv(field.name))
		if *field.dest == "" {
			return cfg, fmt.Errorf("%s: required", field.name)
		}
	}
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" ||
		(endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return cfg, fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT: required HTTP(S) URL without credentials, query or fragment")
	}
	if cfg.Protocol != "http/protobuf" && cfg.Protocol != "grpc" {
		return cfg, fmt.Errorf("OTEL_EXPORTER_OTLP_PROTOCOL: required http/protobuf or grpc")
	}
	return cfg, nil
}
