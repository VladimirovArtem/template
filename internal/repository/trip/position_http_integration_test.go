//go:build integration

package trip_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/VladimirovArtem/template/api"
	transport "github.com/VladimirovArtem/template/internal/http"
	"github.com/VladimirovArtem/template/internal/http/health"
	"github.com/VladimirovArtem/template/internal/http/middleware"
	positioncreatehandler "github.com/VladimirovArtem/template/internal/http/position/create"
	positionlisthandler "github.com/VladimirovArtem/template/internal/http/position/list"
	"github.com/VladimirovArtem/template/internal/http/response"
	createhandler "github.com/VladimirovArtem/template/internal/http/trip/create"
	finishhandler "github.com/VladimirovArtem/template/internal/http/trip/finish"
	gethandler "github.com/VladimirovArtem/template/internal/http/trip/get"
	positionlist "github.com/VladimirovArtem/template/internal/usecase/position/list"
	"github.com/VladimirovArtem/template/internal/usecase/trip/create"
	"github.com/VladimirovArtem/template/internal/usecase/trip/finish"
	"github.com/VladimirovArtem/template/internal/usecase/trip/get"
)

func TestPositionsHTTP(t *testing.T) {
	const invalidRequest = "invalid_request"
	ctx := testContext(t)
	repo, tx, created := positionFixture(t)
	now := time.Date(2026, 11, 1, 9, 0, 0, 123456000, time.UTC)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	writer := response.New(logger)
	instrumentation, err := middleware.New(logger, writer, tracenoop.NewTracerProvider(), metricnoop.NewMeterProvider())
	if err != nil {
		t.Fatal(err)
	}
	router, err := transport.NewRouter(
		createhandler.New(create.New(repo, tx, uuid.New, time.Now, testEvents), writer),
		gethandler.New(get.New(repo), writer), finishhandler.New(finish.New(repo, tx, time.Now, testEvents), writer),
		positioncreatehandler.New(newPositionSaver(t, repo, tx, func() time.Time { return now }), writer),
		positionlisthandler.New(positionlist.New(repo), writer), health.New(testPool, queryTimeout, writer), writer, instrumentation,
	)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/trips/" + created.ID.String()
	positionsPath := path + "/positions"
	result := positionHTTPRequest(t, ctx, router, http.MethodGet, positionsPath, "", http.StatusOK)
	if strings.TrimSpace(result.Body.String()) != `{"positions":[]}` {
		t.Fatalf("empty route: %s", result.Body)
	}
	latest := now.Add(time.Minute)
	saved := make([]api.TripPosition, 0, 2)
	for _, at := range []time.Time{latest, now.Add(-time.Minute)} {
		result = positionHTTPRequest(t, ctx, router, http.MethodPost, positionsPath, positionJSON(at), http.StatusCreated)
		var point api.TripPosition
		if err := json.Unmarshal(result.Body.Bytes(), &point); err != nil {
			t.Fatal(err)
		}
		if point.Id <= 0 || point.TripId != created.ID || !point.RecordedAt.Equal(at) {
			t.Fatalf("saved point: %+v", point)
		}
		saved = append(saved, point)
	}
	result = positionHTTPRequest(t, ctx, router, http.MethodGet, positionsPath, "", http.StatusOK)
	var route api.TripPositionsResponse
	if err := json.Unmarshal(result.Body.Bytes(), &route); err != nil {
		t.Fatal(err)
	}
	if len(route.Positions) != 2 || route.Positions[0].Id != saved[1].Id || route.Positions[1].Id != saved[0].Id {
		t.Fatalf("sorted HTTP route: %+v", route)
	}
	for _, operation := range []struct{ method, path string }{{http.MethodGet, path}, {http.MethodPost, path + "/finish"}} {
		result = positionHTTPRequest(t, ctx, router, operation.method, operation.path, "", http.StatusOK)
		var found api.Trip
		if err := json.Unmarshal(result.Body.Bytes(), &found); err != nil {
			t.Fatal(err)
		}
		if found.LastPositionAt == nil || !found.LastPositionAt.Equal(latest) {
			t.Fatalf("HTTP last position: %s", result.Body)
		}
	}
	positionHTTPRequest(t, ctx, router, http.MethodGet, positionsPath, "", http.StatusOK)
	for _, tt := range []struct {
		method, path, body, code string
		status                   int
	}{
		{http.MethodPost, positionsPath, positionJSON(now), "trip_completed", http.StatusConflict},
		{http.MethodPost, positionsPath, positionJSON(latest.Add(time.Nanosecond)), invalidRequest, http.StatusBadRequest},
		{http.MethodPost, positionsPath, positionJSON(time.Time{}), invalidRequest, http.StatusBadRequest},
		{http.MethodGet, "/api/v1/trips/" + uuid.Nil.String() + "/positions", "", invalidRequest, http.StatusBadRequest},
		{http.MethodPost, "/api/v1/trips/" + uuid.Nil.String() + "/positions", positionJSON(now), invalidRequest, http.StatusBadRequest},
		{http.MethodGet, "/api/v1/trips/" + uuid.NewString() + "/positions", "", "trip_not_found", http.StatusNotFound},
		{http.MethodPost, "/api/v1/trips/" + uuid.NewString() + "/positions", positionJSON(now), "trip_not_found", http.StatusNotFound},
	} {
		result = positionHTTPRequest(t, ctx, router, tt.method, tt.path, tt.body, tt.status)
		var problem api.Problem
		if err := json.Unmarshal(result.Body.Bytes(), &problem); err != nil {
			t.Fatal(err)
		}
		if problem.Code != tt.code || int(problem.Status) != tt.status || problem.Instance == nil || *problem.Instance != tt.path {
			t.Fatalf("HTTP problem: %s", result.Body)
		}
	}
}

func positionJSON(at time.Time) string {
	return fmt.Sprintf(`{"coordinates":{"latitude":0,"longitude":0},"recorded_at":%q}`, at.Format(time.RFC3339Nano))
}

func positionHTTPRequest(t *testing.T, ctx context.Context, router http.Handler, method, path, body string, status int) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	contentType := "application/json"
	if status >= http.StatusBadRequest {
		contentType = "application/problem+json"
	}
	if recorder.Code != status || recorder.Header().Get("Content-Type") != contentType {
		t.Fatalf("%s %s: %d %s, want %d", method, path, recorder.Code, recorder.Body, status)
	}
	return recorder
}
