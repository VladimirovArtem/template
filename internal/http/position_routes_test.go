package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/VladimirovArtem/template/api"
	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/model"
)

const positionBody = `{"coordinates":{"latitude":0,"longitude":0},"recorded_at":"2026-11-01T09:00:00Z"}`

func TestCreatePositionRoute(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		lat, lon float64
		pad      bool
	}{
		{name: "zero coordinates"},
		{name: "minimum coordinates", lat: -90, lon: -180},
		{name: "maximum coordinates", lat: 90, lon: 180},
		{name: "exact body limit", pad: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			router := newTestRouter(t)
			id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
			at := time.Date(2026, 11, 1, 9, 0, 0, 123456000, time.UTC)
			input := model.CreatePositionInput{TripID: id, Coordinates: trip.Coordinates{Latitude: tt.lat, Longitude: tt.lon}, RecordedAt: at}
			body := fmt.Sprintf(`{"coordinates":{"latitude":%g,"longitude":%g},"recorded_at":%q}`, tt.lat, tt.lon, at.Format(time.RFC3339Nano))
			if tt.pad {
				body += strings.Repeat(" ", (1<<20)-len(body))
			}
			created := trip.Position{ID: 9007199254740993, TripID: id, Coordinates: input.Coordinates, RecordedAt: at, CreatedAt: at.Add(time.Second)}
			ctx := context.WithValue(t.Context(), requestKey{}, t.Name())
			router.createPosition.EXPECT().Execute(gomock.Any(), input).DoAndReturn(func(got context.Context, _ model.CreatePositionInput) (trip.Position, error) {
				if got.Value(requestKey{}) != t.Name() || got.Done() != ctx.Done() {
					t.Fatal("request context values or cancellation were lost")
				}
				return created, nil
			})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/trips/"+id.String()+"/positions", strings.NewReader(body)).WithContext(ctx)
			request.Header.Set("Content-Type", jsonContentType)
			recorder := httptest.NewRecorder()
			router.handler.ServeHTTP(recorder, request)
			var result api.TripPosition
			if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			want := api.TripPosition{Id: created.ID, TripId: id, Coordinates: api.Coordinates{Latitude: tt.lat, Longitude: tt.lon}, RecordedAt: at}
			if recorder.Code != http.StatusCreated || recorder.Header().Get("Content-Type") != jsonContentType || !reflect.DeepEqual(result, want) ||
				strings.Contains(recorder.Body.String(), "created_at") {
				t.Fatalf("create response: %d %s", recorder.Code, recorder.Body)
			}
		})
	}
}

func TestListPositionsRoute(t *testing.T) {
	t.Parallel()
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprintf("empty=%t", empty), func(t *testing.T) {
			t.Parallel()
			router := newTestRouter(t)
			id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
			at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
			points := []trip.Position{{ID: 42, TripID: id, RecordedAt: at}, {ID: 43, TripID: id, RecordedAt: at}}
			want := []api.TripPosition{{Id: 42, TripId: id, RecordedAt: at}, {Id: 43, TripId: id, RecordedAt: at}}
			if empty {
				points, want = nil, []api.TripPosition{}
			}
			ctx := context.WithValue(t.Context(), requestKey{}, t.Name())
			router.listPositions.EXPECT().Execute(gomock.Any(), id).DoAndReturn(func(got context.Context, _ uuid.UUID) ([]trip.Position, error) {
				if got.Value(requestKey{}) != t.Name() || got.Done() != ctx.Done() {
					t.Fatal("request context values or cancellation were lost")
				}
				return points, nil
			})
			request := httptest.NewRequest(http.MethodGet, "/api/v1/trips/"+id.String()+"/positions", nil).WithContext(ctx)
			recorder := httptest.NewRecorder()
			router.handler.ServeHTTP(recorder, request)
			var result api.TripPositionsResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != jsonContentType || !reflect.DeepEqual(result.Positions, want) {
				t.Fatalf("list response: %d %s", recorder.Code, recorder.Body)
			}
		})
	}
}

func TestPositionProblems(t *testing.T) {
	t.Parallel()
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		for _, tt := range []struct {
			name, code string
			err        error
			status     int
		}{
			{"missing trip", "trip_not_found", fmt.Errorf("lookup: %w", trip.ErrNotFound), http.StatusNotFound},
			{"invalid recorded_at", invalidRequestCode, &trip.ValidationError{Field: "recorded_at", Reason: "future"}, http.StatusBadRequest},
			{"internal error", "internal_error", errors.New("SELECT secret FROM database"), http.StatusInternalServerError},
			{"completed trip", "trip_completed", fmt.Errorf("update: %w", trip.ErrCompleted), http.StatusConflict},
		} {
			if method == http.MethodGet && tt.status == http.StatusConflict {
				continue
			}
			t.Run(method+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				router := newTestRouter(t)
				id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
				if method == http.MethodPost {
					router.createPosition.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(trip.Position{}, tt.err)
				} else {
					router.listPositions.EXPECT().Execute(gomock.Any(), id).Return(nil, tt.err)
				}
				body := ""
				if method == http.MethodPost {
					body = positionBody
				}
				request := httptest.NewRequest(method, "/api/v1/trips/"+id.String()+"/positions", strings.NewReader(body))
				request.Header.Set("Content-Type", jsonContentType)
				recorder := httptest.NewRecorder()
				router.handler.ServeHTTP(recorder, request)
				var problem api.Problem
				if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
					t.Fatal(err)
				}
				if recorder.Code != tt.status || recorder.Header().Get("Content-Type") != problemContentType ||
					int(problem.Status) != tt.status || problem.Code != tt.code || problem.Instance == nil || *problem.Instance != request.URL.Path ||
					strings.Contains(recorder.Body.String(), "secret") {
					t.Fatalf("position problem: %d %s", recorder.Code, recorder.Body)
				}
			})
		}
	}
}
