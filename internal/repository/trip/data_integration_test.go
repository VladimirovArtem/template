//go:build integration

package trip_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/gateway/push"
	"github.com/VladimirovArtem/template/internal/infra/postgres/transaction"
	"github.com/VladimirovArtem/template/internal/infra/telemetry"
	"github.com/VladimirovArtem/template/internal/model"
	repository "github.com/VladimirovArtem/template/internal/repository/trip"
	positioncreate "github.com/VladimirovArtem/template/internal/usecase/position/create"
	"github.com/VladimirovArtem/template/internal/worker/positions"
)

func newPositionSaver(t *testing.T, repo *repository.Repository, tx *transaction.Manager, now func() time.Time) *positioncreate.Usecase {
	t.Helper()
	meters, traces := metricnoop.NewMeterProvider(), tracenoop.NewTracerProvider()
	observer, err := telemetry.NewPositions(meters)
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := push.New("http://push.invalid", queryTimeout, traces, meters)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := positions.New(gateway, repo, 1, 1, queryTimeout, slog.New(slog.NewJSONHandler(io.Discard, nil)), observer, traces.Tracer("test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		if err := pool.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		gateway.CloseIdleConnections()
	})
	return positioncreate.New(repo, tx, now, pool, observer)
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	t.Cleanup(cancel)
	return ctx
}

func newInput(t *testing.T) model.CreateTripInput {
	t.Helper()
	input := model.CreateTripInput{
		UserID:     uuid.New(),
		DriverID:   uuid.New(),
		StartPoint: trip.Coordinates{Latitude: 59.9, Longitude: 30.3},
		EndPoint:   trip.Coordinates{Latitude: 60.1, Longitude: 30.5},
		Price:      9007199254740993,
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		if _, err := testPool.Exec(ctx, "DELETE FROM trips WHERE driver_id = $1", input.DriverID); err != nil {
			t.Errorf("delete test trips with cascading history and positions: %v", err)
		}
	})
	return input
}

type historyRow struct {
	ID         int64
	FromStatus *trip.Status
	ToStatus   trip.Status
	Reason     *string
	ChangedAt  time.Time
}

func readHistory(t *testing.T, ctx context.Context, id uuid.UUID) []historyRow {
	t.Helper()
	rows, err := testPool.Query(ctx, "SELECT id, from_status, to_status, reason, changed_at FROM trip_status_history WHERE trip_id = $1 ORDER BY id", id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var history []historyRow
	for rows.Next() {
		var entry historyRow
		if err := rows.Scan(&entry.ID, &entry.FromStatus, &entry.ToStatus, &entry.Reason, &entry.ChangedAt); err != nil {
			t.Fatal(err)
		}
		history = append(history, entry)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return history
}
