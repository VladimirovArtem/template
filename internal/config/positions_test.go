package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadPositions(t *testing.T) {
	const checkIntervalKey = "POSITION_CHECK_INTERVAL"
	const pushURLKey = "PUSH_HTTP_URL"
	const staleAfterKey = "POSITION_STALE_AFTER"
	const workersKey = "POSITION_WORKERS"
	defaults := map[string]string{
		checkIntervalKey: "30s", staleAfterKey: "60s", workersKey: "4",
		"POSITION_QUEUE_SIZE": "100", "POSITION_SCAN_BATCH_SIZE": "100", pushURLKey: "https://push.invalid", "PUSH_REQUEST_TIMEOUT": "1s",
	}
	for _, tt := range []struct{ name, key, value string }{
		{name: "valid"},
		{"interval missing", checkIntervalKey, ""},
		{"interval zero", checkIntervalKey, "0s"},
		{"interval invalid", checkIntervalKey, "bad"},
		{"stale negative", staleAfterKey, "-1s"},
		{"stale precision", staleAfterKey, "1ns"},
		{"workers zero", workersKey, "0"},
		{"workers fraction", workersKey, "1.5"},
		{"queue negative", "POSITION_QUEUE_SIZE", "-1"},
		{"batch missing", "POSITION_SCAN_BATCH_SIZE", ""},
		{"timeout zero", "PUSH_REQUEST_TIMEOUT", "0s"},
		{"URL missing", pushURLKey, ""},
		{"relative URL", pushURLKey, "/push"},
		{"URL scheme", pushURLKey, "ftp://push.invalid"},
		{"URL missing host", pushURLKey, "http://"},
		{"URL credentials", pushURLKey, "http://user:secret-value@push.invalid"},
		{"URL query", pushURLKey, "https://push.invalid?token=secret-value"},
		{"URL fragment", pushURLKey, "https://push.invalid#secret-value"},
		{"URL bad port", pushURLKey, "https://push.invalid:65536"},
		{"URL empty port", pushURLKey, "https://push.invalid:"},
		{"URL bad syntax", pushURLKey, "https://[secret-value"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for key, value := range defaults {
				t.Setenv(key, value)
			}
			if tt.key != "" {
				t.Setenv(tt.key, tt.value)
			}
			cfg, err := loadPositions()
			if tt.key != "" {
				if err == nil || !strings.Contains(err.Error(), tt.key) || strings.Contains(err.Error(), "secret-value") {
					t.Fatalf("configuration error: %v", err)
				}
				return
			}
			if err != nil || cfg.CheckInterval != 30*time.Second || cfg.StaleAfter != time.Minute || cfg.Workers != 4 || cfg.QueueSize != 100 ||
				cfg.ScanBatchSize != 100 || cfg.PushURL != defaults[pushURLKey] || cfg.RequestTimeout != time.Second {
				t.Fatalf("positions configuration: %+v, %v", cfg, err)
			}
		})
	}
}
