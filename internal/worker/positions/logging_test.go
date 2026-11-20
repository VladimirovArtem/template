package positions

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/VladimirovArtem/template/internal/model"
)

type testHTTPError struct{}

func (testHTTPError) Error() string   { return "private response payload" }
func (testHTTPError) HTTPStatus() int { return 503 }

func TestPoolLogsFailureOnce(t *testing.T) {
	t.Parallel()
	ctx := poolContext(t)
	pool, gateway := newTestPool(t, 1, 1, testBudget)
	var logs bytes.Buffer
	pool.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	var sent model.PositionRequest
	gateway.EXPECT().RequestPosition(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, input model.PositionRequest) error {
		sent = input
		return testHTTPError{}
	})
	pool.Put(ctx, testTrip(1, time.Now()))
	if err := pool.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	var failures, starts, stops int
	data := logs.String()
	decoder := json.NewDecoder(strings.NewReader(data))
	for decoder.More() {
		var record struct {
			Message    string `json:"msg"`
			Level      string `json:"level"`
			RequestID  string `json:"request_id"`
			TripID     string `json:"trip_id"`
			HTTPStatus int    `json:"http.status_code"`
		}
		if err := decoder.Decode(&record); err != nil {
			t.Fatal(err)
		}
		switch record.Message {
		case "position workers started":
			starts++
		case "position workers stopped":
			stops++
		case "position request failed":
			failures++
			if record.Level != "WARN" || record.HTTPStatus != 503 || record.TripID != sent.TripID.String() || record.RequestID != sent.RequestID.String() {
				t.Fatalf("failure log: %+v", record)
			}
		}
	}
	if failures != 1 || starts != 1 || stops != 1 || strings.Contains(data, "private response payload") {
		t.Fatalf("log counts: failures=%d starts=%d stops=%d", failures, starts, stops)
	}
}
