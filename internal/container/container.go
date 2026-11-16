package container

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"

	"github.com/VladimirovArtem/template/internal/config"
	transport "github.com/VladimirovArtem/template/internal/http"
	"github.com/VladimirovArtem/template/internal/http/health"
	"github.com/VladimirovArtem/template/internal/http/middleware"
	"github.com/VladimirovArtem/template/internal/http/response"
	createhandler "github.com/VladimirovArtem/template/internal/http/trip/create"
	finishhandler "github.com/VladimirovArtem/template/internal/http/trip/finish"
	gethandler "github.com/VladimirovArtem/template/internal/http/trip/get"
	"github.com/VladimirovArtem/template/internal/infra/postgres/transaction"
	"github.com/VladimirovArtem/template/internal/infra/telemetry"
	"github.com/VladimirovArtem/template/internal/logging"
	triprepo "github.com/VladimirovArtem/template/internal/repository/trip"
	"github.com/VladimirovArtem/template/internal/usecase/trip/create"
	"github.com/VladimirovArtem/template/internal/usecase/trip/finish"
	"github.com/VladimirovArtem/template/internal/usecase/trip/get"
)

type Container struct {
	server          *http.Server
	pool            *pgxpool.Pool
	logger          *slog.Logger
	shutdownTimeout time.Duration
	telemetry       *telemetry.Providers
}

func New(ctx context.Context, cfg config.Config, logger *slog.Logger) (_ *Container, err error) {
	providers, err := telemetry.New(ctx, cfg.Telemetry)
	if err != nil {
		return nil, fmt.Errorf("initialize telemetry: %w", err)
	}
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		logger.LogAttrs(ctx, slog.LevelWarn, "telemetry export failed", logging.ErrorAttrs(err)...)
	}))
	c := &Container{
		logger:          logger,
		shutdownTimeout: cfg.ShutdownTimeout,
		telemetry:       providers,
		server: &http.Server{
			Addr:              cfg.HTTPAddr,
			ReadTimeout:       cfg.ReadTimeout,
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
			WriteTimeout:      cfg.WriteTimeout,
			IdleTimeout:       cfg.IdleTimeout,
			ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
			BaseContext:       func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
		},
	}
	defer func() {
		if err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.shutdownTimeout)
			defer cancel()
			err = errors.Join(err, c.shutdown(cleanupCtx))
		}
	}()
	dbTracer, err := telemetry.NewPostgresTracer(providers.Traces, providers.Metrics)
	if err != nil {
		return nil, err
	}
	poolConfig := cfg.Pool.Copy()
	poolConfig.ConnConfig.Tracer = dbTracer
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool: %w", databaseError{cause: err})
	}
	c.pool = pool
	repo := triprepo.New(pool, cfg.QueryTimeout)
	tx := transaction.New(pool, cfg.QueryTimeout, logger)
	trips, err := telemetry.NewTrips(providers.Metrics, logger)
	if err != nil {
		return nil, err
	}
	if err := telemetry.RegisterActiveTrips(providers.Metrics, repo, cfg.QueryTimeout); err != nil {
		return nil, fmt.Errorf("register active trip gauge: %w", err)
	}
	writer := response.New(logger)
	instrumentation, err := middleware.New(logger, writer, providers.Traces, providers.Metrics)
	if err != nil {
		return nil, err
	}
	c.server.Handler, err = transport.NewRouter(
		createhandler.New(create.New(repo, tx, uuid.New, time.Now, trips), writer),
		gethandler.New(get.New(repo), writer),
		finishhandler.New(finish.New(repo, tx, time.Now, trips), writer),
		health.New(pool, cfg.QueryTimeout, writer), writer, instrumentation,
	)
	if err != nil {
		return nil, fmt.Errorf("initialize HTTP routes: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, cfg.Pool.ConnConfig.ConnectTimeout)
	err = pool.Ping(pingCtx)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("PostgreSQL startup ping failed (check DATABASE_URL and availability): %w", databaseError{cause: err})
	}
	return c, nil
}
