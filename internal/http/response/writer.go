package response

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/VladimirovArtem/template/api"
	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/http/domainerror"
	"github.com/VladimirovArtem/template/internal/http/requeststate"
	"github.com/VladimirovArtem/template/internal/logging"
)

type Writer struct {
	logger *slog.Logger
}

func New(logger *slog.Logger) *Writer {
	return &Writer{logger: logger}
}

func (w *Writer) JSON(dst http.ResponseWriter, r *http.Request, status int, body any) {
	w.write(dst, r, status, "application/json", body)
}

func (w *Writer) Problem(dst http.ResponseWriter, r *http.Request, status int, body api.Problem) {
	instance := r.URL.Path
	body.Instance = &instance
	w.write(dst, r, status, "application/problem+json", body)
}

func (w *Writer) Error(dst http.ResponseWriter, r *http.Request, operation string, err error) {
	status, body := domainerror.Problem(err)
	span := trace.SpanFromContext(r.Context())
	span.SetAttributes(attribute.String("error.type", body.Code))
	if status == http.StatusInternalServerError {
		w.RecordError(r, operation, err)
		span.SetStatus(codes.Error, "request failed")
	}
	w.Problem(dst, r, status, body)
}

func (w *Writer) InvalidRequest(dst http.ResponseWriter, r *http.Request) {
	w.Error(dst, r, "validate request", trip.ErrInvalidInput)
}

func (w *Writer) RecordError(r *http.Request, operation string, err error) {
	if !requeststate.RecordError(r.Context(), operation, err) {
		attrs := append([]slog.Attr{slog.String("operation", operation)}, logging.ErrorAttrs(err)...)
		w.logger.LogAttrs(r.Context(), slog.LevelError, "operation failed", attrs...)
	}
}

func (w *Writer) write(dst http.ResponseWriter, r *http.Request, status int, contentType string, body any) {
	dst.Header().Set("Content-Type", contentType)
	dst.WriteHeader(status)
	if err := json.NewEncoder(dst).Encode(body); err != nil {
		w.RecordError(r, "write HTTP response", err)
	}
}
