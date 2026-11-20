package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Positions struct {
	CheckInterval  time.Duration
	StaleAfter     time.Duration
	RequestTimeout time.Duration
	Workers        int
	QueueSize      int
	ScanBatchSize  int
	PushURL        string
}

func loadPositions() (Positions, error) {
	var cfg Positions
	for _, setting := range []struct {
		name string
		dest *time.Duration
	}{
		{"POSITION_CHECK_INTERVAL", &cfg.CheckInterval}, {"POSITION_STALE_AFTER", &cfg.StaleAfter}, {"PUSH_REQUEST_TIMEOUT", &cfg.RequestTimeout},
	} {
		value, err := time.ParseDuration(os.Getenv(setting.name))
		if err != nil || value <= 0 {
			return cfg, fmt.Errorf("%s: required positive Go duration", setting.name)
		}
		*setting.dest = value
	}
	if cfg.StaleAfter%time.Microsecond != 0 {
		return cfg, fmt.Errorf("POSITION_STALE_AFTER: must be a whole number of microseconds")
	}
	for _, setting := range []struct {
		name string
		dest *int
	}{
		{"POSITION_WORKERS", &cfg.Workers}, {"POSITION_QUEUE_SIZE", &cfg.QueueSize}, {"POSITION_SCAN_BATCH_SIZE", &cfg.ScanBatchSize},
	} {
		value, err := strconv.Atoi(os.Getenv(setting.name))
		if err != nil || value <= 0 {
			return cfg, fmt.Errorf("%s: required positive integer", setting.name)
		}
		*setting.dest = value
	}
	cfg.PushURL = os.Getenv("PUSH_HTTP_URL")
	u, err := url.Parse(cfg.PushURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || strings.HasSuffix(u.Host, ":") ||
		(u.Port() != "" && !validPort(u.Port())) {
		return cfg, fmt.Errorf("PUSH_HTTP_URL: required absolute HTTP(S) URL without credentials, query or fragment; port must be in 1..65535")
	}
	return cfg, nil
}
