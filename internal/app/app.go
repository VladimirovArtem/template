package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/VladimirovArtem/template/internal/postgres"
	"github.com/VladimirovArtem/template/internal/trip"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrShutdownTimeout = errors.New("shutdown budget exhausted")

func Run(ctx context.Context, cfg Config, logger *slog.Logger) (result error) {
	pool, err := pgxpool.NewWithConfig(ctx, cfg.Pool)
	if err != nil {
		return fmt.Errorf("create PostgreSQL pool: %w", databaseError{cause: err})
	}
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		ReadTimeout:       cfg.ReadTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
		BaseContext: func(net.Listener) context.Context {

			return context.WithoutCancel(ctx)
		},
	}
	defer func() {
		logger.Info("service stopping")
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
		defer cancel()
		if err := shutdown(shutdownCtx, server, pool); err != nil {
			result = errors.Join(result, err)
		} else {
			logger.Info("service stopped")
		}
	}()
	repo := postgres.NewTrips(pool, cfg.QueryTimeout)
	tx := postgres.NewTxManager(pool, cfg.QueryTimeout, logger)
	server.Handler, err = newRouter(pool, cfg.QueryTimeout, logger, trip.NewService(repo, tx))
	if err != nil {
		return fmt.Errorf("initialize HTTP routes: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, cfg.Pool.ConnConfig.ConnectTimeout)
	err = pool.Ping(pingCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("PostgreSQL startup ping failed (check DATABASE_URL and availability): %w", databaseError{cause: err})
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen on HTTP_ADDR: %w", err)
	}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()
	logger.InfoContext(ctx, "HTTP server started", "addr", listener.Addr().String())
	select {
	case <-ctx.Done():
		return nil
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	}
}

func shutdown(ctx context.Context, server *http.Server, pool *pgxpool.Pool) error {
	done := make(chan error, 1)
	go func() {
		err := server.Shutdown(ctx)
		if err != nil {
			_ = server.Close()
		}

		pool.Close()
		done <- err
	}()
	select {
	case <-ctx.Done():
		return ErrShutdownTimeout
	case err := <-done:
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
			return ErrShutdownTimeout
		}
		if err != nil {
			return fmt.Errorf("shutdown HTTP: %w", err)
		}
		return nil
	}
}

type databaseError struct {
	cause error
}

func (e databaseError) Error() string {
	if errors.Is(e.cause, context.DeadlineExceeded) || pgconn.Timeout(e.cause) {
		return "database operation timed out"
	}
	if errors.Is(e.cause, context.Canceled) {
		return "database operation canceled"
	}
	var pgErr *pgconn.PgError
	if errors.As(e.cause, &pgErr) {
		return "PostgreSQL error, SQLSTATE " + pgErr.Code
	}
	return "database connection failed"
}

func (e databaseError) Unwrap() error { return e.cause }
