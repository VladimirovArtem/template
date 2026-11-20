package positions

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"

	"github.com/VladimirovArtem/template/internal/gateway/push"
)

func TestPositionTraceLinksAndHTTP(t *testing.T) {
	t.Parallel()
	requests := make(chan trace.SpanContext, 2)
	release := make(chan struct{}, 2)
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		ctx := propagation.TraceContext{}.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		requests <- trace.SpanContextFromContext(ctx)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusAccepted)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	t.Cleanup(server.Close)
	h := newScanHarness(t, 2, func(h *scanHarness) pushGateway {
		gateway, err := push.New(server.URL, testBudget, h.traces, h.meters)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(gateway.CloseIdleConnections)
		return gateway
	})
	h.signals.requests = make(chan string, 2)
	h.repo.EXPECT().ListStaleTrips(gomock.Any(), gomock.Any(), time.Minute, 100, nil).Times(2).Return(scanTrips(h.now, 1), nil)
	var wire []trace.SpanContext
	for _, expected := range []string{"success", "error"} {
		if h.scan(t) != 1 {
			t.Fatal("wrong row count")
		}
		select {
		case sc := <-requests:
			wire = append(wire, sc)
		case <-h.ctx.Done():
			t.Fatal(h.ctx.Err())
		}
		metrics := readPositionMetrics(t, h.ctx, h.reader)
		if gaugeValue(metrics, "tripgo.position_requests.workers.active") != 1 || gaugeValue(metrics, "tripgo.position_requests.queue.size") != 0 {
			t.Fatal("gauge did not show actual in-flight HTTP")
		}
		release <- struct{}{}
		select {
		case result := <-h.signals.requests:
			if result != expected {
				t.Fatalf("request result=%s", result)
			}
		case <-h.ctx.Done():
			t.Fatal(h.ctx.Err())
		}
	}
	if err := h.worker.Shutdown(h.ctx); err != nil {
		t.Fatal(err)
	}
	assertPositionTraces(t, h.exporter.GetSpans(), wire)
	assertPositionRequestMetrics(t, readPositionMetrics(t, h.ctx, h.reader))
	t.Log("two independent linked worker roots, two child HTTP spans, matching W3C traceparent; success=1 error=1, two real durations, gauges zero")
}

func TestClosedQueueIsNotOverflow(t *testing.T) {
	t.Parallel()
	h := newScanHarness(t, 1)
	if err := h.pool.Shutdown(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.repo.EXPECT().ListStaleTrips(gomock.Any(), h.now, time.Minute, 100, nil).Return(scanTrips(h.now, 100), nil)
	if h.scan(t) != 100 {
		t.Fatal("returned rows were not counted")
	}
	metrics := readPositionMetrics(t, h.ctx, h.reader)
	if countResult(metrics, "tripgo.stale_checks", "dropped") != 0 || countResult(metrics, "tripgo.position_requests", "cancelled") != 0 {
		t.Fatal("closed queue fabricated dropped/cancelled")
	}
}

func assertPositionTraces(t *testing.T, spans tracetest.SpanStubs, wire []trace.SpanContext) {
	t.Helper()
	var checks, jobs, httpSpans tracetest.SpanStubs
	for _, span := range spans {
		switch span.Name {
		case "positions.check":
			checks = append(checks, span)
		case "positions.request":
			jobs = append(jobs, span)
		case "POST /api/v1/push":
			httpSpans = append(httpSpans, span)
		}
	}
	if len(checks) != 2 || len(jobs) != 2 || len(httpSpans) != 2 || len(spans) != 6 {
		t.Fatalf("span counts: checks=%d jobs=%d HTTP=%d total=%d", len(checks), len(jobs), len(httpSpans), len(spans))
	}
	for i, job := range jobs {
		if job.Parent.IsValid() || len(job.Links) != 1 || job.Links[0].SpanContext.SpanID() != checks[i].SpanContext.SpanID() || job.SpanContext.TraceID() == checks[i].SpanContext.TraceID() {
			t.Fatalf("job is a child instead of linked root: %+v", job)
		}
		httpSpan := httpSpans[i]
		if httpSpan.Parent.SpanID() != job.SpanContext.SpanID() || httpSpan.SpanContext.TraceID() != job.SpanContext.TraceID() || httpSpan.SpanKind != trace.SpanKindClient ||
			wire[i].SpanID() != httpSpan.SpanContext.SpanID() || wire[i].TraceID() != httpSpan.SpanContext.TraceID() {
			t.Fatal("HTTP span or W3C traceparent lost worker parent")
		}
	}
	if jobs[1].Status.Code != codes.Error || httpSpans[1].Status.Code != codes.Error {
		t.Fatal("HTTP failure missing from trace")
	}
	attrs := attribute.NewSet(jobs[1].Attributes...)
	status, _ := attrs.Value("http.response.status_code")
	if status.AsInt64() != http.StatusServiceUnavailable {
		t.Fatal("HTTP status missing from worker diagnosis")
	}
}

func assertPositionRequestMetrics(t *testing.T, metrics map[string]metricdata.Metrics) {
	t.Helper()
	if countResult(metrics, "tripgo.position_requests", "success") != 1 || countResult(metrics, "tripgo.position_requests", "error") != 1 || countResult(metrics, "tripgo.position_requests", "cancelled") != 0 {
		t.Fatal("wrong request counters")
	}
	duration := metrics["tripgo.position_requests.duration"]
	points := duration.Data.(metricdata.Histogram[float64]).DataPoints
	if duration.Unit != "s" || len(points) != 2 || points[0].Count != 1 || points[1].Count != 1 || points[0].Sum < 0 || points[1].Sum < 0 {
		t.Fatalf("HTTP durations: %+v", duration)
	}
	if gaugeValue(metrics, "tripgo.position_requests.queue.size") != 0 || gaugeValue(metrics, "tripgo.position_requests.workers.active") != 0 {
		t.Fatal("gauges did not return to zero")
	}
	for name, item := range metrics {
		if name != "tripgo.stale_checks" && name != "tripgo.position_requests" {
			continue
		}
		for _, point := range item.Data.(metricdata.Sum[int64]).DataPoints {
			if point.Attributes.Len() != 1 {
				t.Fatalf("unbounded labels on %s: %v", name, point.Attributes.ToSlice())
			}
		}
	}
}
