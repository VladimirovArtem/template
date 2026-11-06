package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/felixge/httpsnoop"
	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/VladimirovArtem/template/internal/http/domainerror"
	"github.com/VladimirovArtem/template/internal/http/requeststate"
	"github.com/VladimirovArtem/template/internal/http/response"
	"github.com/VladimirovArtem/template/internal/logging"
)

type HTTP struct {
	logger *slog.Logger
	writer *response.Writer
	traces trace.TracerProvider
	meters metric.MeterProvider
	active metric.Int64UpDownCounter
}

func New(logger *slog.Logger, writer *response.Writer, traces trace.TracerProvider, meters metric.MeterProvider) (*HTTP, error) {
	active, err := meters.Meter("tripgo/http").Int64UpDownCounter("http.server.active_requests", metric.WithUnit("{request}"))
	if err != nil {
		return nil, fmt.Errorf("create active request counter: %w", err)
	}
	return &HTTP{logger: logger, writer: writer, traces: traces, meters: meters, active: active}, nil
}

func (m *HTTP) Wrap(handler http.Handler, router *chi.Mux) http.Handler {
	guarded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Recover inside otelhttp as well so it records the recovered response, including its 500 duration.
		defer m.recover(w, r)
		handler.ServeHTTP(w, r)
	})
	traced := otelhttp.NewHandler(m.log(m.metrics(guarded)), "", otelhttp.WithTracerProvider(m.traces),
		otelhttp.WithMeterProvider(m.meters), otelhttp.WithPropagators(propagation.TraceContext{}),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return requestMethod(r) + " " + requeststate.FromContext(r.Context()).Route
		}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := "/unmatched"
		routeContext := chi.NewRouteContext()
		if router.Match(routeContext, r.Method, r.URL.Path) {
			route = routeContext.RoutePattern()
		}
		state := &requeststate.State{Route: route, Status: http.StatusOK}
		r = r.WithContext(requeststate.NewContext(r.Context(), state))
		r.Pattern = requestMethod(r) + " " + route
		w = httpsnoop.Wrap(w, httpsnoop.Hooks{
			WriteHeader: func(next httpsnoop.WriteHeaderFunc) httpsnoop.WriteHeaderFunc {
				return func(status int) {
					if !state.WroteHeader && status >= http.StatusOK {
						state.Status, state.WroteHeader = status, true
					}
					next(status)
				}
			},
			Write: func(next httpsnoop.WriteFunc) httpsnoop.WriteFunc {
				return func(data []byte) (int, error) {
					state.WroteHeader = true
					return next(data)
				}
			},
		})
		defer m.recover(w, r)
		traced.ServeHTTP(w, r)
	})
}

func (m *HTTP) log(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		state := requeststate.FromContext(r.Context())
		trace.SpanFromContext(r.Context()).SetAttributes(attribute.String("http.route", state.Route))
		defer func() {
			level := slog.LevelInfo
			if state.Status >= http.StatusInternalServerError {
				level = slog.LevelError
			}
			attrs := []slog.Attr{
				slog.String("method", requestMethod(r)), slog.String("route", state.Route),
				slog.Int("status", state.Status), slog.Float64("duration_ms", float64(time.Since(started))/float64(time.Millisecond)),
			}
			if state.Err != nil {
				attrs = append(attrs, slog.String("operation", state.Operation))
				attrs = append(attrs, logging.ErrorAttrs(state.Err)...)
			}
			if len(state.Stack) != 0 {
				attrs = append(attrs, slog.String("stack", string(state.Stack)))
			}
			m.logger.LogAttrs(r.Context(), level, "request handled", attrs...)
		}()
		next.ServeHTTP(w, r)
	})
}

func (m *HTTP) metrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attrs := metric.WithAttributes(attribute.String("http.request.method", requestMethod(r)),
			attribute.String("http.route", requeststate.FromContext(r.Context()).Route))
		m.active.Add(r.Context(), 1, attrs)
		defer m.active.Add(r.Context(), -1, attrs)
		next.ServeHTTP(w, r)
	})
}

func (m *HTTP) recover(w http.ResponseWriter, r *http.Request) {
	if recovered := recover(); recovered != nil {
		state := requeststate.FromContext(r.Context())
		state.Operation, state.Err, state.Stack = "handle request", logging.ErrPanic, debug.Stack()
		span := trace.SpanFromContext(r.Context())
		span.SetStatus(codes.Error, "request panicked")
		span.RecordError(logging.ErrPanic)
		span.SetAttributes(attribute.String("error.type", "panic"))
		if !state.WroteHeader {
			status, body := domainerror.Problem(logging.ErrPanic)
			m.writer.Problem(w, r, status, body)
		}
	}
}

func requestMethod(r *http.Request) string {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodDelete,
		http.MethodConnect, http.MethodOptions, http.MethodTrace, http.MethodPatch:
		return r.Method
	default:
		return "_OTHER"
	}
}
