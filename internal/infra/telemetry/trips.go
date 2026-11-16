package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/metric"
)

type Trips struct {
	created   metric.Int64Counter
	completed metric.Int64Counter
	logger    *slog.Logger
}

func NewTrips(provider metric.MeterProvider, logger *slog.Logger) (*Trips, error) {
	meter := provider.Meter("tripgo/trips")
	created, err := meter.Int64Counter("tripgo.trips.created", metric.WithUnit("{trip}"))
	if err != nil {
		return nil, fmt.Errorf("create trip counter: %w", err)
	}
	completed, err := meter.Int64Counter("tripgo.trips.completed", metric.WithUnit("{trip}"))
	if err != nil {
		return nil, fmt.Errorf("complete trip counter: %w", err)
	}
	return &Trips{created: created, completed: completed, logger: logger}, nil
}

func (t *Trips) Created(ctx context.Context, id uuid.UUID) {
	t.created.Add(ctx, 1)
	t.logger.InfoContext(ctx, "trip created", "trip_id", id)
}

func (t *Trips) Completed(ctx context.Context, id uuid.UUID) {
	t.completed.Add(ctx, 1)
	t.logger.InfoContext(ctx, "trip completed", "trip_id", id)
}

func RegisterActiveTrips(provider metric.MeterProvider, repo activeTrips, timeout time.Duration) error {
	meter := provider.Meter("tripgo/trips")
	_, err := meter.Int64ObservableGauge("tripgo.trips.active", metric.WithUnit("{trip}"), metric.WithInt64Callback(
		func(ctx context.Context, observer metric.Int64Observer) error {
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			count, err := repo.CountActive(ctx)
			if err != nil {
				return fmt.Errorf("observe active trips: %w", err)
			}
			observer.Observe(count)
			return nil
		},
	))
	return err
}
