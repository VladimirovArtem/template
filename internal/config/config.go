package config

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	HTTPAddr          string
	LogLevel          slog.Level
	ShutdownTimeout   time.Duration
	ReadTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	QueryTimeout      time.Duration
	Pool              *pgxpool.Config
	Telemetry         Telemetry
}

func Load() (Config, error) {
	var cfg Config
	cfg.HTTPAddr = os.Getenv("HTTP_ADDR")
	host, port, err := net.SplitHostPort(cfg.HTTPAddr)
	if err != nil || !validHost(host) || !validPort(port) {
		return cfg, fmt.Errorf("HTTP_ADDR: required host:port with a port in 1..65535 (for example :8080)")
	}
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		cfg.LogLevel = slog.LevelDebug
	case "info":
		cfg.LogLevel = slog.LevelInfo
	case "warn":
		cfg.LogLevel = slog.LevelWarn
	case "error":
		cfg.LogLevel = slog.LevelError
	default:
		return cfg, fmt.Errorf("LOG_LEVEL: required debug, info, warn or error")
	}
	var connectTimeout, lifetime time.Duration
	for _, setting := range []struct {
		name string
		dest *time.Duration
	}{
		{"SHUTDOWN_TIMEOUT", &cfg.ShutdownTimeout},
		{"HTTP_READ_TIMEOUT", &cfg.ReadTimeout},
		{"HTTP_READ_HEADER_TIMEOUT", &cfg.ReadHeaderTimeout},
		{"HTTP_WRITE_TIMEOUT", &cfg.WriteTimeout},
		{"HTTP_IDLE_TIMEOUT", &cfg.IdleTimeout},
		{"DATABASE_CONNECT_TIMEOUT", &connectTimeout},
		{"DATABASE_QUERY_TIMEOUT", &cfg.QueryTimeout},
		{"DATABASE_MAX_CONN_LIFETIME", &lifetime},
	} {
		value, err := time.ParseDuration(os.Getenv(setting.name))
		if err != nil || value <= 0 {
			return cfg, fmt.Errorf("%s: required positive Go duration (for example 5s)", setting.name)
		}
		*setting.dest = value
	}
	if cfg.ReadHeaderTimeout > cfg.ReadTimeout {
		return cfg, fmt.Errorf("HTTP_READ_HEADER_TIMEOUT: must not exceed HTTP_READ_TIMEOUT")
	}
	if cfg.QueryTimeout >= cfg.WriteTimeout {
		return cfg, fmt.Errorf("DATABASE_QUERY_TIMEOUT: must be less than HTTP_WRITE_TIMEOUT")
	}
	if lifetime <= connectTimeout {
		return cfg, fmt.Errorf("DATABASE_MAX_CONN_LIFETIME: must exceed DATABASE_CONNECT_TIMEOUT")
	}
	maxConns, err := connectionCount("DATABASE_MAX_CONNS", 1)
	if err != nil {
		return cfg, err
	}
	minConns, err := connectionCount("DATABASE_MIN_CONNS", 0)
	if err != nil {
		return cfg, err
	}
	if minConns > maxConns {
		return cfg, fmt.Errorf("DATABASE_MIN_CONNS: must not exceed DATABASE_MAX_CONNS")
	}
	pool, err := parseDatabaseURL(os.Getenv("DATABASE_URL"))
	if err != nil {
		return cfg, err
	}
	pool.MaxConns = maxConns
	pool.MinConns = minConns
	pool.MaxConnLifetime = lifetime
	pool.ConnConfig.ConnectTimeout = connectTimeout
	cfg.Pool = pool
	cfg.Telemetry, err = loadTelemetry()
	if err != nil {
		return cfg, err
	}
	return cfg, nil
}

func connectionCount(name string, minimum int64) (int32, error) {
	value, err := strconv.ParseInt(os.Getenv(name), 10, 32)
	if err != nil || value < minimum {
		return 0, fmt.Errorf("%s: required integer in %d..2147483647", name, minimum)
	}
	return int32(value), nil
}

func validPort(port string) bool {
	value, err := strconv.ParseUint(port, 10, 16)
	return err == nil && value > 0
}

func validHost(host string) bool {
	if host == "" || net.ParseIP(host) != nil {
		return true
	}
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			letter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
			digit := c >= '0' && c <= '9'
			if !letter && !digit && c != '-' {
				return false
			}
		}
	}
	return true
}
