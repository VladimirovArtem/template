package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/VladimirovArtem/template/api"
)

func TestHealth(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	newTestRouter(t).handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	var body api.HealthResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != 200 || recorder.Header().Get("Content-Type") != jsonContentType || body.Status != api.Ok {
		t.Fatalf("health response: %d %s", recorder.Code, recorder.Body)
	}
}

func TestReady(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		err      error
		canceled bool
		status   int
		body     api.HealthResponseStatus
	}{
		{name: "available", status: 200, body: api.Ok},
		{name: "unavailable", err: errors.New("database secret"), status: 503, body: api.Unavailable},
		{name: "canceled request", canceled: true, err: context.Canceled, status: 503, body: api.Unavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			router := newTestRouter(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.canceled {
				cancel()
			}
			started := time.Now()
			var pingCtx context.Context
			router.ping.EXPECT().Ping(gomock.Any()).DoAndReturn(func(got context.Context) error {
				pingCtx = got
				deadline, exists := got.Deadline()
				if !exists || deadline.Before(started.Add(readyTimeout)) || deadline.After(time.Now().Add(readyTimeout)) {
					t.Fatalf("readiness deadline: %v, exists=%t", deadline, exists)
				}
				if tt.canceled && !errors.Is(got.Err(), context.Canceled) {
					t.Fatal("request cancellation was lost")
				}
				return tt.err
			})
			recorder := httptest.NewRecorder()
			router.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/ready", nil).WithContext(ctx))
			var body api.HealthResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != tt.status || recorder.Header().Get("Content-Type") != jsonContentType || body.Status != tt.body {
				t.Fatalf("ready response: %d %s", recorder.Code, recorder.Body)
			}
			if pingCtx == nil || !errors.Is(pingCtx.Err(), context.Canceled) {
				t.Fatal("readiness context was not released")
			}
		})
	}
}
