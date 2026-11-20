package push

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/VladimirovArtem/template/internal/model"
)

const gatewayTestBudget = 20 * time.Second

type auditedTransport struct {
	*http.Transport
	instrumented http.RoundTripper
	attempts     atomic.Int64
	readBytes    atomic.Int64
	closed       atomic.Int64
	replayable   atomic.Bool
}

func (a *auditedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	a.attempts.Add(1)
	if request.GetBody != nil {
		a.replayable.Store(true)
	}
	response, err := a.instrumented.RoundTrip(request)
	if response != nil {
		response.Body = &auditedBody{ReadCloser: response.Body, audit: a}
	}
	return response, err
}

type auditedBody struct {
	io.ReadCloser
	audit *auditedTransport
}

func (b *auditedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.audit.readBytes.Add(int64(n))
	return n, err
}

func (b *auditedBody) Close() error {
	b.audit.closed.Add(1)
	return b.ReadCloser.Close()
}

func newAuditedGateway(t *testing.T, baseURL string, timeout time.Duration) (*Gateway, *auditedTransport) {
	t.Helper()
	gateway, err := New(baseURL, timeout, tracenoop.NewTracerProvider(), metricnoop.NewMeterProvider())
	if err != nil {
		t.Fatal(err)
	}
	audit := &auditedTransport{Transport: gateway.transport, instrumented: gateway.client.Transport}
	gateway.client.Transport = audit
	t.Cleanup(gateway.CloseIdleConnections)
	return gateway, audit
}

func gatewayContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), gatewayTestBudget)
	t.Cleanup(cancel)
	return ctx
}

func testPositionRequest() model.PositionRequest {
	return model.PositionRequest{RequestID: uuid.New(), TripID: uuid.New(), DriverID: uuid.New()}
}

func TestGatewayRequestAndStatuses(t *testing.T) {
	t.Parallel()
	for _, status := range []int{202, 400, 429, 503, 200, 302, 307} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()
			input := testPositionRequest()
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/push" || r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("wrong push request: %s %s", r.Method, r.URL)
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				var requestID, recipient, kind string
				var data map[string]string
				for key, destination := range map[string]any{"request_id": &requestID, "recipient_id": &recipient, "kind": &kind, "data": &data} {
					if err := json.Unmarshal(body[key], destination); err != nil {
						t.Error(err)
					}
				}
				if len(body) != 4 || requestID != input.RequestID.String() || recipient != input.DriverID.String() || kind != "REQUEST_POSITION" ||
					len(data) != 1 || data["trip_id"] != input.TripID.String() {
					t.Errorf("wrong push JSON: %s", body)
				}
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, strings.Repeat("private response payload", maxResponseBytes))
			}))
			t.Cleanup(server.Close)
			gateway, audit := newAuditedGateway(t, server.URL+"/", gatewayTestBudget)
			err := gateway.RequestPosition(gatewayContext(t), input)
			if status == http.StatusAccepted {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var statusErr *HTTPError
				if !errors.As(err, &statusErr) || statusErr.StatusCode != status || statusErr.HTTPStatus() != status ||
					!strings.Contains(err.Error(), strconv.Itoa(status)) || strings.Contains(err.Error(), "private response payload") {
					t.Fatalf("HTTP error lost status or included response: %v", err)
				}
			}
			if calls.Load() != 1 || audit.attempts.Load() != 1 || audit.closed.Load() != 1 || audit.readBytes.Load() > maxResponseBytes || audit.replayable.Load() {
				t.Fatalf("attempts=%d calls=%d closes=%d bytes=%d replayable=%t", audit.attempts.Load(), calls.Load(), audit.closed.Load(), audit.readBytes.Load(), audit.replayable.Load())
			}
		})
	}
}

func TestGatewayCancellation(t *testing.T) {
	t.Parallel()
	ctx := gatewayContext(t)
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	gateway, audit := newAuditedGateway(t, server.URL, gatewayTestBudget)
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- gateway.RequestPosition(requestCtx, testPositionRequest()) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancel()
	select {
	case err := <-done:
		var httpErr *HTTPError
		if !errors.Is(err, context.Canceled) || errors.As(err, &httpErr) || audit.attempts.Load() != 1 {
			t.Fatalf("canceled request: %v, attempts=%d", err, audit.attempts.Load())
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestGatewayTimeout(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	gateway, audit := newAuditedGateway(t, server.URL, 20*time.Millisecond)
	err := gateway.RequestPosition(gatewayContext(t), testPositionRequest())
	var httpErr *HTTPError
	var network net.Error
	if !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &network) || !network.Timeout() || errors.As(err, &httpErr) || audit.attempts.Load() != 1 {
		t.Fatalf("request timeout: %v, attempts=%d", err, audit.attempts.Load())
	}
}

func TestGatewayTransportError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("closed server received request") }))
	server.Close()
	gateway, audit := newAuditedGateway(t, server.URL, gatewayTestBudget)
	err := gateway.RequestPosition(gatewayContext(t), testPositionRequest())
	var httpErr *HTTPError
	var network net.Error
	if err == nil || !errors.As(err, &network) || errors.As(err, &httpErr) || audit.attempts.Load() != 1 {
		t.Fatalf("transport error: %v, attempts=%d", err, audit.attempts.Load())
	}
}

func TestGatewayCloseIdleConnections(t *testing.T) {
	t.Parallel()
	ctx := gatewayContext(t)
	closed := make(chan struct{}, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"message_id":"accepted","accepted_at":"2026-11-01T12:00:00Z"}`)
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			closed <- struct{}{}
		}
	}
	server.Start()
	t.Cleanup(server.Close)
	gateway, _ := newAuditedGateway(t, server.URL, gatewayTestBudget)
	if err := gateway.RequestPosition(ctx, testPositionRequest()); err != nil {
		t.Fatal(err)
	}
	gateway.CloseIdleConnections()
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("idle connection did not close")
	}
}

func TestGatewayInvalidConfiguration(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, base string
		timeout    time.Duration
	}{
		{"empty URL", "", time.Second},
		{"relative URL", "/push", time.Second},
		{"unsupported scheme", "ftp://example.invalid", time.Second},
		{"missing host", "http://", time.Second},
		{"empty hostname", "http://:8080", time.Second},
		{"invalid syntax", "http://[::1", time.Second},
		{"invalid port", "http://example.invalid:no", time.Second},
		{"zero port", "http://example.invalid:0", time.Second},
		{"large port", "http://example.invalid:65536", time.Second},
		{"credentials", "http://user:password@example.invalid", time.Second},
		{"query", "http://example.invalid?key=value", time.Second},
		{"empty query", "http://example.invalid?", time.Second},
		{"fragment", "http://example.invalid#fragment", time.Second},
		{"zero timeout", "https://example.invalid", 0},
		{"negative timeout", "https://example.invalid", -time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if gateway, err := New(tt.base, tt.timeout, tracenoop.NewTracerProvider(), metricnoop.NewMeterProvider()); err == nil || gateway != nil {
				t.Fatalf("New = %+v, %v", gateway, err)
			}
		})
	}
}
