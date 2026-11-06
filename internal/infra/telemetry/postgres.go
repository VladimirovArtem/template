package telemetry

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"

	"github.com/VladimirovArtem/template/internal/logging"
)

type queryKey struct{}

type queryInfo struct {
	started time.Time
	attrs   []attribute.KeyValue
}

type PostgresTracer struct {
	*otelpgx.Tracer
	duration metric.Float64Histogram
	table    *regexp.Regexp
}

func NewPostgresTracer(traces trace.TracerProvider, metrics metric.MeterProvider) (*PostgresTracer, error) {
	duration, err := metrics.Meter("tripgo/postgres").Float64Histogram("db.client.operation.duration", metric.WithUnit("s"))
	if err != nil {
		return nil, fmt.Errorf("create database duration histogram: %w", err)
	}
	return &PostgresTracer{
		// otelpgx lacks collection/error metric labels; emit the catalog histogram once at the pool boundary.
		Tracer: otelpgx.NewTracer(otelpgx.WithTracerProvider(traces), otelpgx.WithMeterProvider(noop.NewMeterProvider()),
			otelpgx.WithDisableSQLStatementInAttributes(), otelpgx.WithDisableConnectionDetailsInAttributes(), otelpgx.WithDisableAcquireTracer()),
		duration: duration,
		table:    regexp.MustCompile(`(?i)\b(?:FROM|INTO|UPDATE)\s+(trips|trip_status_history)\b`),
	}, nil
}

func (t *PostgresTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	attrs := []attribute.KeyValue{attribute.String("db.operation.name", otelpgx.SQLOperationName(ctx, data.SQL))}
	if match := t.table.FindStringSubmatch(data.SQL); len(match) == 2 {
		attrs = append(attrs, attribute.String("db.collection.name", strings.ToLower(match[1])))
	}
	ctx = context.WithValue(ctx, queryKey{}, queryInfo{started: time.Now(), attrs: attrs})
	ctx = t.Tracer.TraceQueryStart(ctx, conn, data)
	trace.SpanFromContext(ctx).SetAttributes(attrs...)
	return ctx
}

func (t *PostgresTracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	info, ok := ctx.Value(queryKey{}).(queryInfo)
	if ok {
		attrs := info.attrs
		if kind := logging.ErrorType(data.Err); kind != "" {
			attrs = append(attrs, attribute.String("error.type", kind))
			span := trace.SpanFromContext(ctx)
			span.SetAttributes(attribute.String("error.type", kind))
			span.SetStatus(codes.Error, logging.SafeError{Cause: data.Err}.Error())
			data.Err = logging.SafeError{Cause: data.Err}
		}
		t.duration.Record(ctx, time.Since(info.started).Seconds(), metric.WithAttributes(attrs...))
	}
	t.Tracer.TraceQueryEnd(ctx, conn, data)
}

func (t *PostgresTracer) TraceConnectEnd(ctx context.Context, data pgx.TraceConnectEndData) {
	if data.Err != nil {
		trace.SpanFromContext(ctx).SetAttributes(attribute.String("error.type", logging.ErrorType(data.Err)))
		data.Err = logging.SafeError{Cause: data.Err}
	}
	t.Tracer.TraceConnectEnd(ctx, data)
}

func (t *PostgresTracer) TraceConnectStart(ctx context.Context, data pgx.TraceConnectStartData) context.Context {
	ctx = t.Tracer.TraceConnectStart(ctx, data)
	trace.SpanFromContext(ctx).SetAttributes(attribute.String("db.operation.name", "CONNECT"))
	return ctx
}

func (t *PostgresTracer) TracePrepareStart(ctx context.Context, conn *pgx.Conn, data pgx.TracePrepareStartData) context.Context {
	ctx = t.Tracer.TracePrepareStart(ctx, conn, data)
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attribute.String("db.operation.name", "PREPARE"))
	if match := t.table.FindStringSubmatch(data.SQL); len(match) == 2 {
		span.SetAttributes(attribute.String("db.collection.name", strings.ToLower(match[1])))
	}
	return ctx
}

func (t *PostgresTracer) TracePrepareEnd(ctx context.Context, conn *pgx.Conn, data pgx.TracePrepareEndData) {
	if data.Err != nil {
		trace.SpanFromContext(ctx).SetAttributes(attribute.String("error.type", logging.ErrorType(data.Err)))
		data.Err = logging.SafeError{Cause: data.Err}
	}
	t.Tracer.TracePrepareEnd(ctx, conn, data)
}
