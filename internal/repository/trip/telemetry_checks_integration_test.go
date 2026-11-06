//go:build integration

package trip_test

import (
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

const tripCollection = "trips"

func checkTraceLink(t *testing.T, spans tracetest.SpanStubs, record map[string]any) (tracetest.SpanStub, tracetest.SpanStub) {
	t.Helper()
	var server, database tracetest.SpanStub
	var serverCount, databaseCount int
	for _, span := range spans {
		if span.SpanKind == trace.SpanKindServer {
			server, serverCount = span, serverCount+1
		}
		attrs := attribute.NewSet(span.Attributes...)
		operation, _ := attrs.Value("db.operation.name")
		collection, _ := attrs.Value("db.collection.name")
		if span.SpanKind == trace.SpanKindClient {
			if operation.AsString() == "" {
				t.Fatal("database operation attribute missing")
			}
			if operation.AsString() == "PREPARE" && collection.AsString() != tripCollection {
				t.Fatal("prepared SQL collection missing")
			}
		}
		if operation.AsString() == "SELECT" && collection.AsString() == tripCollection {
			database, databaseCount = span, databaseCount+1
		}
	}
	if serverCount != 1 || databaseCount != 1 {
		t.Fatalf("server spans=%d, SELECT trip spans=%d", serverCount, databaseCount)
	}
	if server.Name != "GET /api/v1/trips/{tripId}" || record["route"] != "/api/v1/trips/{tripId}" ||
		record["trace_id"] != server.SpanContext.TraceID().String() || record["span_id"] != server.SpanContext.SpanID().String() ||
		database.SpanContext.TraceID() != server.SpanContext.TraceID() || database.Parent.SpanID() != server.SpanContext.SpanID() {
		t.Fatalf("uncorrelated telemetry: log=%v, HTTP=%+v, DB=%+v", record, server, database)
	}
	return server, database
}

func checkDatabaseDuration(t *testing.T, metrics map[string]metricdata.Metrics, id string) {
	t.Helper()
	dbDuration := metrics["db.client.operation.duration"]
	var selectCount uint64
	for _, point := range dbDuration.Data.(metricdata.Histogram[float64]).DataPoints {
		operation, _ := point.Attributes.Value("db.operation.name")
		collection, _ := point.Attributes.Value("db.collection.name")
		if operation.AsString() == "SELECT" && collection.AsString() == tripCollection {
			selectCount += point.Count
		}
		if strings.Contains(point.Attributes.Encoded(attribute.DefaultEncoder()), id) {
			t.Fatal("UUID in database metric labels")
		}
	}
	if dbDuration.Unit != "s" || selectCount != 1 {
		t.Fatalf("SELECT duration count=%d, metric=%+v", selectCount, dbDuration)
	}
}

func checkFailedDatabaseSpan(t *testing.T, spans tracetest.SpanStubs, driverID string) {
	t.Helper()
	var failedQueries int
	for _, span := range spans {
		attrs := attribute.NewSet(span.Attributes...)
		kind, _ := attrs.Value("error.type")
		if kind.AsString() == "23505" {
			failedQueries++
			if span.Status.Code != codes.Error || strings.Contains(span.Status.Description, driverID) {
				t.Fatalf("unsafe error span: %+v", span)
			}
		}
		for _, attr := range span.Attributes {
			if attr.Key == "db.query.text" || attr.Key == "pgx.query.parameters" {
				t.Fatalf("SQL text or parameters exported: %s", attr.Key)
			}
		}
	}
	if failedQueries != 1 {
		t.Fatalf("failed query spans=%d", failedQueries)
	}
	for _, span := range spans {
		for _, event := range span.Events {
			for _, attr := range event.Attributes {
				if strings.Contains(attr.Value.String(), driverID) {
					t.Fatal("SQL parameter leaked in error event")
				}
			}
		}
	}
}
