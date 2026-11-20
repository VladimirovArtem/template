package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type Positions struct {
	saved    metric.Int64Counter
	checks   metric.Int64Counter
	rows     metric.Int64Histogram
	requests metric.Int64Counter
	duration metric.Float64Histogram
}

func NewPositions(provider metric.MeterProvider) (*Positions, error) {
	meter := provider.Meter("tripgo/positions")
	saved, err := meter.Int64Counter("tripgo.positions.saved", metric.WithUnit("{position}"))
	if err != nil {
		return nil, fmt.Errorf("create saved position counter: %w", err)
	}
	checks, err := meter.Int64Counter("tripgo.stale_checks", metric.WithUnit("{trip}"))
	if err != nil {
		return nil, fmt.Errorf("create stale check counter: %w", err)
	}
	rows, err := meter.Int64Histogram("tripgo.stale_checks.rows_read", metric.WithUnit("{row}"))
	if err != nil {
		return nil, fmt.Errorf("create scanned row histogram: %w", err)
	}
	requests, err := meter.Int64Counter("tripgo.position_requests", metric.WithUnit("{request}"))
	if err != nil {
		return nil, fmt.Errorf("create position request counter: %w", err)
	}
	duration, err := meter.Float64Histogram("tripgo.position_requests.duration", metric.WithUnit("s"))
	if err != nil {
		return nil, fmt.Errorf("create position request histogram: %w", err)
	}
	return &Positions{saved: saved, checks: checks, rows: rows, requests: requests, duration: duration}, nil
}

func (p *Positions) Saved(ctx context.Context) { p.saved.Add(ctx, 1) }

func (p *Positions) Checked(ctx context.Context, result string) {
	p.checks.Add(ctx, 1, metric.WithAttributes(attribute.String("result", result)))
}

func (p *Positions) RowsRead(ctx context.Context, count int) { p.rows.Record(ctx, int64(count)) }

func (p *Positions) Requested(ctx context.Context, result string, duration time.Duration) {
	attrs := metric.WithAttributes(attribute.String("result", result))
	p.requests.Add(ctx, 1, attrs)
	p.duration.Record(ctx, duration.Seconds(), attrs)
}

func (p *Positions) Canceled(ctx context.Context) {
	p.requests.Add(ctx, 1, metric.WithAttributes(attribute.String("result", "cancelled")))
}

func RegisterPositionState(provider metric.MeterProvider, state positionState) error {
	meter := provider.Meter("tripgo/positions")
	queue, err := meter.Int64ObservableGauge("tripgo.position_requests.queue.size", metric.WithUnit("{task}"))
	if err != nil {
		return fmt.Errorf("create position queue gauge: %w", err)
	}
	workers, err := meter.Int64ObservableGauge("tripgo.position_requests.workers.active", metric.WithUnit("{worker}"))
	if err != nil {
		return fmt.Errorf("create active worker gauge: %w", err)
	}
	_, err = meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
		pending, active := state.Snapshot()
		observer.ObserveInt64(queue, int64(pending))
		observer.ObserveInt64(workers, int64(active))
		return nil
	}, queue, workers)
	if err != nil {
		return fmt.Errorf("register position gauges: %w", err)
	}
	return nil
}
