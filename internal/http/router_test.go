package http_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/mock/gomock"

	"github.com/VladimirovArtem/template/api"
	"github.com/VladimirovArtem/template/internal/domain/trip"
	transport "github.com/VladimirovArtem/template/internal/http"
	"github.com/VladimirovArtem/template/internal/http/health"
	"github.com/VladimirovArtem/template/internal/http/middleware"
	"github.com/VladimirovArtem/template/internal/http/response"
	"github.com/VladimirovArtem/template/internal/http/trip/create"
	"github.com/VladimirovArtem/template/internal/http/trip/finish"
	"github.com/VladimirovArtem/template/internal/http/trip/get"
)

type testRouter struct {
	handler http.Handler
	create  *Mockcreator
	get     *MocktripUsecase
	finish  *MocktripUsecase
	ping    *Mockpinger
}

type requestKey struct{}

const (
	readyTimeout       = 10 * time.Second
	jsonContentType    = "application/json"
	problemContentType = "application/problem+json"
	finishOperation    = "finish"
	invalidUUID        = "invalid"
)

func newTestRouter(t *testing.T) testRouter {
	t.Helper()
	ctrl := gomock.NewController(t)
	r := testRouter{create: NewMockcreator(ctrl), get: NewMocktripUsecase(ctrl), finish: NewMocktripUsecase(ctrl), ping: NewMockpinger(ctrl)}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	writer := response.New(logger)
	instrumentation, err := middleware.New(logger, writer, tracenoop.NewTracerProvider(), metricnoop.NewMeterProvider())
	if err != nil {
		t.Fatal(err)
	}
	r.handler, err = transport.NewRouter(create.New(r.create, writer), get.New(r.get, writer), finish.New(r.finish, writer),
		health.New(r.ping, readyTimeout, writer), writer, instrumentation)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestTripRoutes(t *testing.T) {
	t.Parallel()
	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	for _, operation := range []string{"get", finishOperation} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			router := newTestRouter(t)
			method, path, dependency := http.MethodGet, "/api/v1/trips/"+id.String(), router.get
			if operation == finishOperation {
				method, path, dependency = "POST", path+"/finish", router.finish
			}
			ctx := context.WithValue(t.Context(), requestKey{}, t.Name())
			request := httptest.NewRequest(method, path, nil).WithContext(ctx)
			found := trip.Trip{ID: id, Status: trip.StatusActive}
			if operation == finishOperation {
				at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
				found.Status, found.FinishedAt = trip.StatusCompleted, &at
			}
			dependency.EXPECT().Execute(gomock.Any(), id).DoAndReturn(func(got context.Context, _ uuid.UUID) (trip.Trip, error) {
				if got.Value(requestKey{}) != t.Name() || got.Done() != ctx.Done() {
					t.Fatal("request context values or cancellation were lost")
				}
				return found, nil
			})
			recorder := httptest.NewRecorder()
			router.handler.ServeHTTP(recorder, request)
			var body api.Trip
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != 200 || recorder.Header().Get("Content-Type") != jsonContentType || body.Id != id || body.Status != api.TripStatus(found.Status) {
				t.Fatalf("route response: %d %s", recorder.Code, recorder.Body)
			}
		})
	}
}

func TestUnknownRoutes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		method, path, code string
		status             int
	}{
		{http.MethodGet, "/unknown", "not_found", 404},
		{"DELETE", "/api/v1/trips", "method_not_allowed", 405},
	} {
		t.Run(tt.code, func(t *testing.T) {
			t.Parallel()
			recorder := httptest.NewRecorder()
			newTestRouter(t).handler.ServeHTTP(recorder, httptest.NewRequest(tt.method, tt.path, nil))
			var body api.Problem
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != tt.status || recorder.Header().Get("Content-Type") != problemContentType || body.Code != tt.code || int(body.Status) != tt.status {
				t.Fatalf("route error: %d %s", recorder.Code, recorder.Body)
			}
		})
	}
}
