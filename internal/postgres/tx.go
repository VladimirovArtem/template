package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/VladimirovArtem/template/internal/trip"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type transactionKey struct{}

type TxManager struct {
	pool    *pgxpool.Pool
	timeout time.Duration
	logger  *slog.Logger
}

var _ trip.TxManager = (*TxManager)(nil)

func NewTxManager(pool *pgxpool.Pool, queryTimeout time.Duration, logger *slog.Logger) *TxManager {
	return &TxManager{pool: pool, timeout: queryTimeout, logger: logger}
}

func (m *TxManager) Do(ctx context.Context, fn func(context.Context) error) (result error) {
	if _, ok := ctx.Value(transactionKey{}).(pgx.Tx); ok {
		if err := ctx.Err(); err != nil {
			return err
		}
		return fn(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		panicked := recover()

		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), min(m.timeout, time.Second))
		defer cleanupCancel()
		rollbackErr := tx.Rollback(cleanupCtx)
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			if panicked != nil {

				m.logger.ErrorContext(cleanupCtx, "rollback failed during panic cleanup", "error_type", fmt.Sprintf("%T", rollbackErr))
			} else {
				result = errors.Join(result, fmt.Errorf("rollback transaction: %w", rollbackErr))
			}
		}
		if panicked != nil {
			panic(panicked)
		}
	}()
	if err := fn(context.WithValue(ctx, transactionKey{}, tx)); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

type executor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func executorFor(ctx context.Context, pool *pgxpool.Pool) executor {
	if tx, ok := ctx.Value(transactionKey{}).(pgx.Tx); ok {
		return tx
	}
	return pool
}
