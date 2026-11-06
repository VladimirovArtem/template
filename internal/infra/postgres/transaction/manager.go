package transaction

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/VladimirovArtem/template/internal/logging"
)

const rollbackTimeout = time.Second

type transactionKey struct{}

type Manager struct {
	pool    *pgxpool.Pool
	timeout time.Duration
	logger  *slog.Logger
}

func New(pool *pgxpool.Pool, queryTimeout time.Duration, logger *slog.Logger) *Manager {
	return &Manager{pool: pool, timeout: queryTimeout, logger: logger}
}

func (m *Manager) Do(ctx context.Context, fn func(context.Context) error) (result error) {
	if _, ok := FromContext(ctx); ok {
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

		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), min(m.timeout, rollbackTimeout))
		defer cleanupCancel()
		rollbackErr := tx.Rollback(cleanupCtx)
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			if panicked != nil {
				attrs := append([]slog.Attr{slog.String("operation", "rollback transaction")}, logging.ErrorAttrs(rollbackErr)...)
				m.logger.LogAttrs(cleanupCtx, slog.LevelError, "rollback failed during panic cleanup", attrs...)
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

func FromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(transactionKey{}).(pgx.Tx)
	return tx, ok
}
