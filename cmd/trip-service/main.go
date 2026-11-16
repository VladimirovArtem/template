package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/VladimirovArtem/template/internal/config"
	"github.com/VladimirovArtem/template/internal/container"
	"github.com/VladimirovArtem/template/internal/localenv"
	"github.com/VladimirovArtem/template/internal/logging"
)

func main() {
	os.Exit(run())
}

func run() int {
	localEnv := flag.Bool("local-env", false, "load .env, then .env.example, without overriding existing environment")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "trip-service")
	if *localEnv {
		if err := localenv.Load(ctx); err != nil {
			logger.ErrorContext(ctx, "configuration failed", "error", err)
			return 1
		}
	}
	cfg, err := config.Load()
	if err != nil {
		logger.ErrorContext(ctx, "configuration failed", "error", err)
		return 1
	}
	logger = slog.New(logging.Handler{Handler: slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel})}).With("service", cfg.Telemetry.ServiceName)
	service, err := container.New(ctx, cfg, logger)
	if err == nil {
		err = service.Run(ctx)
	}
	if err != nil {
		if errors.Is(err, container.ErrShutdownTimeout) {
			logger.Error("SHUTDOWN_TIMEOUT exhausted; forcing process exit")
		} else {
			logger.Error("service failed", "error", err)
		}
		return 1
	}
	return 0
}
