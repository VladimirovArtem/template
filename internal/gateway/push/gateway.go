package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/VladimirovArtem/template/internal/model"
)

const maxResponseBytes = 4 << 10

type Gateway struct {
	endpoint  string
	client    *http.Client
	transport *http.Transport
}

func New(baseURL string, timeout time.Duration, traces trace.TracerProvider, meters metric.MeterProvider) (*Gateway, error) {
	if timeout <= 0 {
		return nil, errors.New("push timeout must be positive")
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse push base URL: %w", err)
	}
	if (base.Scheme != "http" && base.Scheme != "https") || base.Hostname() == "" || base.User != nil ||
		base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || base.Opaque != "" {
		return nil, errors.New("push base URL must be an absolute HTTP(S) URL without credentials, query or fragment")
	}
	if base.Port() != "" {
		port, err := strconv.Atoi(base.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, errors.New("push URL port must be between 1 and 65535")
		}
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: timeout}).DialContext,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		IdleConnTimeout:       timeout,
	}
	return &Gateway{
		endpoint:  base.JoinPath("api/v1/push").String(),
		transport: transport,
		client: &http.Client{
			Transport: otelhttp.NewTransport(transport, otelhttp.WithTracerProvider(traces), otelhttp.WithMeterProvider(meters),
				otelhttp.WithPropagators(propagation.TraceContext{}), otelhttp.WithSpanNameFormatter(func(string, *http.Request) string { return "POST /api/v1/push" })),
			Timeout: timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (g *Gateway) RequestPosition(ctx context.Context, input model.PositionRequest) error {
	body := struct {
		RequestID   uuid.UUID         `json:"request_id"`
		RecipientID uuid.UUID         `json:"recipient_id"`
		Kind        string            `json:"kind"`
		Data        map[string]string `json:"data"`
	}{RequestID: input.RequestID, RecipientID: input.DriverID, Kind: "REQUEST_POSITION", Data: map[string]string{"trip_id": input.TripID.String()}}
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode position push: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("create position push request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.GetBody = nil
	response, err := g.client.Do(request)
	if err != nil {
		return fmt.Errorf("send position push: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
	if response.StatusCode != http.StatusAccepted {
		return &HTTPError{StatusCode: response.StatusCode}
	}
	return nil
}

func (g *Gateway) CloseIdleConnections() {
	g.transport.CloseIdleConnections()
}
