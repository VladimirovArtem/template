package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/VladimirovArtem/template/internal/app"
	"github.com/VladimirovArtem/template/internal/localenv"
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
	cfg, err := app.LoadConfig()
	if err != nil {
		logger.ErrorContext(ctx, "configuration failed", "error", err)
		return 1
	}
	logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel})).With("service", "trip-service")
	if err := app.Run(ctx, cfg, logger); err != nil {
		if errors.Is(err, app.ErrShutdownTimeout) {
			logger.Error("SHUTDOWN_TIMEOUT exhausted; forcing process exit")
		} else {
			logger.Error("service failed", "error", err)
		}
		return 1
	}
	return 0
}
