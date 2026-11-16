package logging_test

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/VladimirovArtem/template/internal/logging"
)

func TestLogLevel(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	logger := slog.New(logging.Handler{Handler: slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelError})}).With("service", "trip-service")
	logger.InfoContext(t.Context(), "filtered")
	if output.Len() != 0 {
		t.Fatal("configured log level was ignored")
	}
	logger.ErrorContext(t.Context(), "kept")
	if output.Len() == 0 {
		t.Fatal("error log was filtered")
	}
}
