package create_test

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
	"github.com/VladimirovArtem/template/internal/http/trip/create"
	"github.com/VladimirovArtem/template/internal/model"
)

const validBody = `{"user_id":"22222222-2222-4222-8222-222222222222","driver_id":"33333333-3333-4333-8333-333333333333","start_point":{"latitude":-90,"longitude":-180},"end_point":{"latitude":90,"longitude":180},"price":9007199254740993}`

func TestServeHTTP(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, code string
		err        error
		status     int
	}{
		{name: "success", status: 201},
		{name: "driver busy", status: 409, code: "driver_busy", err: fmt.Errorf("create: %w", trip.ErrDriverBusy)},
		{name: "validation", status: 400, code: "invalid_request", err: &trip.ValidationError{Field: "price", Reason: "invalid"}},
		{name: "internal error", status: 500, code: "internal_error", err: errors.New("SELECT secret FROM database")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			usecase := NewMockusecase(gomock.NewController(t))
			writer := response.New(slog.New(slog.NewJSONHandler(io.Discard, nil)))
			input := model.CreateTripInput{
				UserID:     uuid.MustParse("22222222-2222-4222-8222-222222222222"),
				DriverID:   uuid.MustParse("33333333-3333-4333-8333-333333333333"),
				StartPoint: trip.Coordinates{Latitude: -90, Longitude: -180},
				EndPoint:   trip.Coordinates{Latitude: 90, Longitude: 180}, Price: 9007199254740993,
			}
			created := trip.Trip{
				ID:     uuid.MustParse("11111111-1111-4111-8111-111111111111"),
				UserID: input.UserID, DriverID: input.DriverID, StartPoint: input.StartPoint, EndPoint: input.EndPoint,
				Price: input.Price, Status: trip.StatusActive, StartedAt: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC),
			}
			request := httptest.NewRequest("POST", "/api/v1/trips", strings.NewReader(validBody)).WithContext(t.Context())
			usecase.EXPECT().Execute(gomock.Eq(request.Context()), input).Return(created, tt.err)
			recorder := httptest.NewRecorder()
			create.New(usecase, writer).ServeHTTP(recorder, request)
			if recorder.Code != tt.status {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
			}
			if tt.err != nil {
				var problem api.Problem
				if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
					t.Fatal(err)
				}
				if recorder.Header().Get("Content-Type") != "application/problem+json" || problem.Code != tt.code ||
					int(problem.Status) != tt.status || problem.Instance == nil || *problem.Instance != request.URL.Path ||
					strings.Contains(recorder.Body.String(), "secret") || recorder.Header().Get("Location") != "" {
					t.Fatalf("invalid problem response: %s", recorder.Body)
				}
				return
			}
			var body api.Trip
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if recorder.Header().Get("Content-Type") != "application/json" || recorder.Header().Get("Location") != "/api/v1/trips/"+created.ID.String() ||
				body.Id != created.ID || body.UserId != input.UserID || body.DriverId != input.DriverID || body.Price != input.Price ||
				body.Status != api.Active || !body.StartedAt.Equal(created.StartedAt) || body.FinishedAt != nil ||
				body.StartPoint.Latitude != -90 || body.StartPoint.Longitude != -180 || body.EndPoint.Latitude != 90 || body.EndPoint.Longitude != 180 {
				t.Fatalf("invalid creation response: %s", recorder.Body)
			}
		})
	}
}

func TestServeHTTPRejectsInvalidJSON(t *testing.T) {
	t.Parallel()
	for _, body := range []string{"{", validBody + `{}`, validBody + `garbage`, strings.Replace(validBody, `"price":9007199254740993`, `"price":1.5`, 1), strings.Replace(validBody, `"price":9007199254740993`, `"price":9223372036854775808`, 1), strings.Replace(validBody, `"price":`, `"unknown":0,"price":`, 1)} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			usecase := NewMockusecase(gomock.NewController(t))
			writer := response.New(slog.New(slog.NewJSONHandler(io.Discard, nil)))
			recorder := httptest.NewRecorder()
			create.New(usecase, writer).ServeHTTP(recorder, httptest.NewRequest("POST", "/api/v1/trips", strings.NewReader(body)))
			var problem api.Problem
			if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != 400 || recorder.Header().Get("Content-Type") != "application/problem+json" || problem.Code != "invalid_request" {
				t.Fatalf("invalid rejection: %d %s", recorder.Code, recorder.Body)
			}
		})
	}
}
