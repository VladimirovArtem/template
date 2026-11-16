//go:build integration

package trip_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/model"
)

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
			t.Errorf("delete test trips and cascading history: %v", err)
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
