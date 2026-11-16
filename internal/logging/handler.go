package logging

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

type Handler struct {
	slog.Handler
}

func (h Handler) Handle(ctx context.Context, record slog.Record) error {
	span := trace.SpanContextFromContext(ctx)
	traceID, spanID := "", ""
	if span.IsValid() {
		traceID, spanID = span.TraceID().String(), span.SpanID().String()
	}
	record.AddAttrs(slog.String("trace_id", traceID), slog.String("span_id", spanID))
	return h.Handler.Handle(ctx, record)
}

func (h Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return Handler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h Handler) WithGroup(name string) slog.Handler {
	return Handler{Handler: h.Handler.WithGroup(name)}
}
