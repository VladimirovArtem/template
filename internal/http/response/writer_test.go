package response_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VladimirovArtem/template/api"
	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/http/response"
)

func TestErrorLogging(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		err  error
		logs int
	}{
		{"business error", trip.ErrDriverBusy, 0},
		{"internal error", errors.New("password=secret"), 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			writer := response.New(slog.New(slog.NewJSONHandler(&logs, nil)))
			recorder := httptest.NewRecorder()
			writer.Error(recorder, httptest.NewRequest("POST", "/api/v1/trips", nil), "create trip", tt.err)
			if strings.Count(logs.String(), "\n") != tt.logs {
				t.Fatalf("log count: %s", &logs)
			}
			if strings.Contains(logs.String(), "secret") || strings.Contains(recorder.Body.String(), "secret") {
				t.Fatal("error details leaked")
			}
			var body api.Problem
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Instance == nil || *body.Instance != "/api/v1/trips" || body.Detail == nil || body.Type == "" || body.Title == "" {
				t.Fatalf("incomplete Problem: %+v", body)
			}
		})
	}
}
