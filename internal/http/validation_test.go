package http_test

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/VladimirovArtem/template/api"
)

const createBody = `{"user_id":"22222222-2222-4222-8222-222222222222","driver_id":"33333333-3333-4333-8333-333333333333","start_point":{"latitude":0,"longitude":0},"end_point":{"latitude":0,"longitude":0},"price":0}`

func TestRejectedCreateRequests(t *testing.T) {
	t.Parallel()
	type testCase struct{ name, body, contentType, key string }
	const invalidCreateCaseCount = 51
	tests := make([]testCase, 0, invalidCreateCaseCount)
	tests = append(tests, []testCase{
		{name: "empty body"},
		{name: "malformed JSON", body: "{"},
		{name: "null body", body: "null"},
		{name: "extra JSON", body: createBody + `{}`},
		{name: "trailing garbage", body: createBody + `garbage`},
		{name: "unknown property", body: strings.Replace(createBody, `"price":0`, `"price":0,"other":1`, 1)},
		{name: "too large body", body: createBody + strings.Repeat(" ", 1<<20)},
		{name: "wrong content type", body: createBody, contentType: "text/plain"},
		{name: "missing content type", body: createBody, contentType: "missing"},
		{name: "invalid header UUID", body: createBody, key: invalidUUID},
		{name: "noncanonical header UUID", body: createBody, key: "44444444444444448444444444444444"},
	}...)

	for _, price := range []string{"-1", "1.5", "1e3", "9223372036854775808", `"10"`, "1e999"} {
		tests = append(tests, testCase{name: "invalid price " + price, body: strings.Replace(createBody, `"price":0`, `"price":`+price, 1)})
	}
	for _, field := range []string{"user_id", "driver_id", "start_point", "end_point", "price"} {
		for _, missing := range []bool{false, true} {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(createBody), &fields); err != nil {
				t.Fatal(err)
			}
			if missing {
				delete(fields, field)
			} else {
				fields[field] = json.RawMessage("null")
			}
			body, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			tests = append(tests, testCase{name: field + "/missing=" + strconv.FormatBool(missing), body: string(body)})
		}
	}
	for _, field := range []string{"user_id", "driver_id"} {
		for _, value := range []string{invalidUUID, "22222222222242228222222222222222", "urn:uuid:22222222-2222-4222-8222-222222222222"} {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(createBody), &fields); err != nil {
				t.Fatal(err)
			}
			fields[field] = json.RawMessage(strconv.Quote(value))
			body, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			tests = append(tests, testCase{name: field + "/" + value, body: string(body)})
		}
	}
	for _, point := range []string{"start_point", "end_point"} {
		for _, value := range []string{`{"longitude":0}`, `{"latitude":0}`, `{"latitude":null,"longitude":0}`, `{"latitude":0,"longitude":null}`, `{"latitude":0,"longitude":0,"other":1}`, `{"latitude":90.1,"longitude":0}`, `{"latitude":-90.1,"longitude":0}`, `{"latitude":0,"longitude":180.1}`, `{"latitude":0,"longitude":-180.1}`} {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(createBody), &fields); err != nil {
				t.Fatal(err)
			}
			fields[point] = json.RawMessage(value)
			body, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			tests = append(tests, testCase{name: point + "/" + value, body: string(body)})
		}
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			router := newTestRouter(t)
			request := httptest.NewRequest("POST", "/api/v1/trips", strings.NewReader(tt.body))
			contentType := tt.contentType
			if contentType == "" {
				contentType = jsonContentType
			}
			if contentType != "missing" {
				request.Header.Set("Content-Type", contentType)
			}
			if tt.key != "" {
				request.Header.Set("Idempotency-Key", tt.key)
			}
			recorder := httptest.NewRecorder()
			router.handler.ServeHTTP(recorder, request)
			var body api.Problem
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != 400 || recorder.Header().Get("Content-Type") != problemContentType ||
				body.Status != 400 || body.Code != "invalid_request" || body.Detail == nil || *body.Detail != "Request validation failed" ||
				body.Instance == nil || *body.Instance != request.URL.Path || recorder.Header().Get("Location") != "" {
				t.Fatalf("invalid rejection: %d %s", recorder.Code, recorder.Body)
			}
		})
	}
}
