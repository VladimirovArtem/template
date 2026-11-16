package logging_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/VladimirovArtem/template/internal/logging"
)

func TestSafeDiagnostics(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		err  error
		kind string
	}{
		{"PostgreSQL", fmt.Errorf("select trip: %w", &pgconn.PgError{Code: "42501", Message: "password=secret", Detail: "private SQL parameters"}), "42501"},
		{"timeout", fmt.Errorf("select trip: %w", context.DeadlineExceeded), "deadline_exceeded"},
		{"canceled", context.Canceled, "canceled"},
		{"unknown", errors.New("postgres://user:secret@database/private"), "unexpected_error"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&output, nil))
			logger.LogAttrs(t.Context(), slog.LevelError, "operation failed", logging.ErrorAttrs(tt.err)...)
			if logging.ErrorType(tt.err) != tt.kind || !strings.Contains(output.String(), tt.kind) || strings.Contains(output.String(), "secret") || strings.Contains(output.String(), "SQL parameters") {
				t.Fatalf("diagnostics: %s", &output)
			}
		})
	}
}
