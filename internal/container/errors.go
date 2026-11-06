package container

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

var ErrShutdownTimeout = errors.New("shutdown budget exhausted")

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
