package http_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VladimirovArtem/template/api"
)

func TestRejectedTripRequests(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"get", finishOperation} {
		for _, tt := range []struct{ name, id, body string }{
			{name: "invalid ID", id: invalidUUID},
			{name: "noncanonical ID", id: "11111111111141118111111111111111"},
			{name: "URN ID", id: "urn:uuid:11111111-1111-4111-8111-111111111111"},
			{name: "unexpected JSON", id: "11111111-1111-4111-8111-111111111111", body: `{}`},
			{name: "unexpected whitespace", id: "11111111-1111-4111-8111-111111111111", body: " "},
		} {
			t.Run(operation+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				method, path := http.MethodGet, "/api/v1/trips/"+tt.id
				if operation == finishOperation {
					method, path = "POST", path+"/finish"
				}
				request := httptest.NewRequest(method, path, strings.NewReader(tt.body))
				request.ContentLength = -1
				recorder := httptest.NewRecorder()
				newTestRouter(t).handler.ServeHTTP(recorder, request)
				var body api.Problem
				if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if recorder.Code != 400 || recorder.Header().Get("Content-Type") != problemContentType || body.Code != "invalid_request" || body.Status != 400 {
					t.Fatalf("invalid rejection: %d %s", recorder.Code, recorder.Body)
				}
			})
		}
	}
}
