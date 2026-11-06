package finish_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/VladimirovArtem/template/api"
	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/http/response"
	"github.com/VladimirovArtem/template/internal/http/trip/finish"
)

func TestHandle(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, code string
		err        error
		status     int
	}{
		{name: "success", status: 200},
		{name: "missing trip", status: 404, code: "trip_not_found", err: fmt.Errorf("lookup: %w", trip.ErrNotFound)},
		{name: "internal error", status: 500, code: "internal_error", err: errors.New("SELECT secret FROM database")},
		{name: "already completed", status: 409, code: "trip_completed", err: fmt.Errorf("finish: %w", trip.ErrCompleted)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			usecase := NewMockusecase(gomock.NewController(t))
			writer := response.New(slog.New(slog.NewJSONHandler(io.Discard, nil)))
			at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
			found := trip.Trip{
				ID:         uuid.MustParse("11111111-1111-4111-8111-111111111111"),
				UserID:     uuid.MustParse("22222222-2222-4222-8222-222222222222"),
				DriverID:   uuid.MustParse("33333333-3333-4333-8333-333333333333"),
				StartPoint: trip.Coordinates{Latitude: 59.9, Longitude: 30.3},
				EndPoint:   trip.Coordinates{Latitude: 60.1, Longitude: 30.5},
				Price:      1450, Status: trip.StatusCompleted, StartedAt: at,
			}
			finishedAt := at.Add(time.Hour)
			found.FinishedAt = &finishedAt
			request := httptest.NewRequest("POST", "/api/v1/trips/"+found.ID.String()+"/finish", nil).WithContext(t.Context())
			usecase.EXPECT().Execute(gomock.Eq(request.Context()), found.ID).Return(found, tt.err)
			recorder := httptest.NewRecorder()
			finish.New(usecase, writer).Handle(recorder, request, found.ID)
			if recorder.Code != tt.status {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
			}
			if recorder.Header().Get("Location") != "" {
				t.Fatal("unexpected Location header")
			}
			if tt.err != nil {
				var problem api.Problem
				if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
					t.Fatal(err)
				}
				if recorder.Header().Get("Content-Type") != "application/problem+json" || problem.Code != tt.code ||
					int(problem.Status) != tt.status || problem.Instance == nil || *problem.Instance != request.URL.Path || strings.Contains(recorder.Body.String(), "secret") {
					t.Fatalf("invalid problem response: %s", recorder.Body)
				}
				return
			}
			var body api.Trip
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if recorder.Header().Get("Content-Type") != "application/json" || body.Id != found.ID || body.UserId != found.UserID ||
				body.DriverId != found.DriverID || body.Price != found.Price || body.Status != api.Completed || !body.StartedAt.Equal(at) ||
				body.StartPoint.Latitude != 59.9 || body.StartPoint.Longitude != 30.3 || body.EndPoint.Latitude != 60.1 || body.EndPoint.Longitude != 30.5 {
				t.Fatalf("invalid trip response: %s", recorder.Body)
			}
			if body.FinishedAt == nil || !body.FinishedAt.Equal(finishedAt) {
				t.Fatalf("finished_at = %v", body.FinishedAt)
			}
		})
	}
}
