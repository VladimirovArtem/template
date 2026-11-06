package logging

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net"
)

var ErrPanic = errors.New("request panic")

func ErrorType(err error) string {
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	var postgres interface{ SQLState() string }
	if errors.As(err, &postgres) {
		code := postgres.SQLState()
		if len(code) == 5 {
			return code
		}
		return "postgresql_error"
	}
	switch {
	case errors.Is(err, ErrPanic):
		return "panic"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, io.EOF):
		return "unexpected_eof"
	}
	var network net.Error
	if errors.As(err, &network) {
		return "connection_error"
	}
	return "unexpected_error"
}

func ErrorAttrs(err error) []slog.Attr {
	kind := ErrorType(err)
	attrs := []slog.Attr{slog.String("error.type", kind)}
	var postgres interface{ SQLState() string }
	if errors.As(err, &postgres) {
		return append(attrs, slog.String("cause", "PostgreSQL rejected the operation"), slog.String("sqlstate", kind))
	}
	return append(attrs, slog.String("cause", SafeError{Cause: err}.Error()))
}

type SafeError struct {
	Cause error
}

func (e SafeError) Error() string {
	switch ErrorType(e.Cause) {
	case "deadline_exceeded":
		return "operation timed out"
	case "canceled":
		return "operation canceled"
	case "connection_error":
		return "connection failed"
	case "unexpected_eof":
		return "connection closed unexpectedly"
	case "panic":
		return "request panicked"
	case "unexpected_error":
		return "unexpected operation failure"
	default:
		return "PostgreSQL error, SQLSTATE " + ErrorType(e.Cause)
	}
}

func (e SafeError) Unwrap() error { return e.Cause }
