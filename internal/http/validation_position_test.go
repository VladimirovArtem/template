package http_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VladimirovArtem/template/api"
)

func TestRejectedPositionBodies(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, body, contentType string }{
		{name: "empty"},
		{name: "malformed", body: "{"},
		{name: nullJSON, body: nullJSON},
		{name: "missing fields", body: "{}"},
		{name: "extra JSON", body: positionBody + `{}`},
		{name: "trailing garbage", body: positionBody + `garbage`},
		{name: "unknown field", body: strings.Replace(positionBody, `"coordinates":`, `"other":1,"coordinates":`, 1)},
		{name: "too large body", body: positionBody + strings.Repeat(" ", 1<<20)},
		{name: "wrong content type", body: positionBody, contentType: "text/plain"},
		{name: "missing content type", body: positionBody, contentType: missingContentType},
		{name: "missing coordinates", body: `{"recorded_at":"2026-11-01T09:00:00Z"}`},
		{name: "null coordinates", body: strings.Replace(positionBody, `{"latitude":0,"longitude":0}`, nullJSON, 1)},
		{name: "missing latitude", body: strings.Replace(positionBody, `"latitude":0,`, ``, 1)},
		{name: "missing longitude", body: strings.Replace(positionBody, `,"longitude":0`, ``, 1)},
		{name: "null latitude", body: strings.Replace(positionBody, `"latitude":0`, `"latitude":null`, 1)},
		{name: "null longitude", body: strings.Replace(positionBody, `"longitude":0`, `"longitude":null`, 1)},
		{name: "unknown coordinate", body: strings.Replace(positionBody, `"latitude":0`, `"latitude":0,"other":1`, 1)},
		{name: "low latitude", body: strings.Replace(positionBody, `"latitude":0`, `"latitude":-90.1`, 1)},
		{name: "high latitude", body: strings.Replace(positionBody, `"latitude":0`, `"latitude":90.1`, 1)},
		{name: "low longitude", body: strings.Replace(positionBody, `"longitude":0`, `"longitude":-180.1`, 1)},
		{name: "high longitude", body: strings.Replace(positionBody, `"longitude":0`, `"longitude":180.1`, 1)},
		{name: "overflow coordinate", body: strings.Replace(positionBody, `"latitude":0`, `"latitude":1e999`, 1)},
		{name: "NaN", body: strings.Replace(positionBody, `"latitude":0`, `"latitude":NaN`, 1)},
		{name: "string coordinate", body: strings.Replace(positionBody, `"latitude":0`, `"latitude":"0"`, 1)},
		{name: "missing recorded_at", body: `{"coordinates":{"latitude":0,"longitude":0}}`},
		{name: "null recorded_at", body: strings.Replace(positionBody, `"2026-11-01T09:00:00Z"`, nullJSON, 1)},
		{name: "invalid date", body: strings.Replace(positionBody, `2026-11-01T09:00:00Z`, `2026-02-30T09:00:00Z`, 1)},
		{name: "empty date", body: strings.Replace(positionBody, `2026-11-01T09:00:00Z`, ``, 1)},
		{name: "date without time", body: strings.Replace(positionBody, `2026-11-01T09:00:00Z`, `2026-11-01`, 1)},
		{name: "date without timezone", body: strings.Replace(positionBody, `2026-11-01T09:00:00Z`, `2026-11-01T09:00:00`, 1)},
		{name: "numeric date", body: strings.Replace(positionBody, `"2026-11-01T09:00:00Z"`, `0`, 1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/trips/11111111-1111-4111-8111-111111111111/positions", strings.NewReader(tt.body))
			if tt.contentType == "" {
				request.Header.Set("Content-Type", jsonContentType)
			} else if tt.contentType != missingContentType {
				request.Header.Set("Content-Type", tt.contentType)
			}
			assertInvalidPositionRequest(t, request)
		})
	}
}

func TestRejectedPositionPaths(t *testing.T) {
	t.Parallel()
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		for _, id := range []string{invalidUUID, "11111111111141118111111111111111", "urn:uuid:11111111-1111-4111-8111-111111111111"} {
			t.Run(method+"/"+id, func(t *testing.T) {
				t.Parallel()
				body := ""
				if method == http.MethodPost {
					body = positionBody
				}
				request := httptest.NewRequest(method, "/api/v1/trips/"+id+"/positions", strings.NewReader(body))
				request.Header.Set("Content-Type", jsonContentType)
				assertInvalidPositionRequest(t, request)
			})
		}
	}
	for _, body := range []string{"{}", " "} {
		t.Run("GET body "+body, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodGet, "/api/v1/trips/11111111-1111-4111-8111-111111111111/positions", strings.NewReader(body))
			request.ContentLength = -1
			assertInvalidPositionRequest(t, request)
		})
	}
}

func assertInvalidPositionRequest(t *testing.T, request *http.Request) {
	t.Helper()
	recorder := httptest.NewRecorder()
	newTestRouter(t).handler.ServeHTTP(recorder, request)
	var problem api.Problem
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusBadRequest || recorder.Header().Get("Content-Type") != problemContentType ||
		problem.Status != http.StatusBadRequest || problem.Code != invalidRequestCode || problem.Instance == nil || *problem.Instance != request.URL.Path {
		t.Fatalf("invalid rejection: %d %s", recorder.Code, recorder.Body)
	}
}
