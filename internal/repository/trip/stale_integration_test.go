//go:build integration

package trip_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/infra/postgres/transaction"
	"github.com/VladimirovArtem/template/internal/model"
	repository "github.com/VladimirovArtem/template/internal/repository/trip"
)

type staleFixture struct {
	status         trip.Status
	startedAt      time.Time
	lastPositionAt *time.Time
}

func TestStaleTripsFilterAndOrder(t *testing.T) {
	ctx := testContext(t)
	now := time.Date(2026, 11, 1, 12, 0, 0, 123456000, time.UTC)
	old, fresh, boundary := now.Add(-2*time.Minute), now.Add(-30*time.Second), now.Add(-time.Minute)
	older := now.Add(-3 * time.Minute)
	fixtures := []staleFixture{
		{status: trip.StatusActive, startedAt: old},
		{status: trip.StatusActive, startedAt: fresh},
		{status: trip.StatusActive, startedAt: boundary},
		{status: trip.StatusActive, startedAt: boundary.Add(-time.Microsecond)},
		{status: trip.StatusActive, startedAt: now.Add(-time.Hour), lastPositionAt: &old},
		{status: trip.StatusActive, startedAt: now.Add(-time.Hour), lastPositionAt: &fresh},
		{status: trip.StatusActive, startedAt: now.Add(-time.Hour), lastPositionAt: &boundary},
		{status: trip.StatusActive, startedAt: fresh, lastPositionAt: &older},
		{status: trip.StatusCompleted, startedAt: old},
		{status: trip.StatusCompleted, startedAt: old, lastPositionAt: &old},
	}
	inserted := insertStaleFixtures(t, ctx, fixtures)
	want := []model.StaleTrip{inserted[0], inserted[3], inserted[4], inserted[7]}
	sort.Slice(want, func(i, j int) bool { return staleTripLess(want[i], want[j]) })
	repo := repository.New(testPool, queryTimeout)
	got, err := repo.ListStaleTrips(ctx, now, time.Minute, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertStaleTrips(t, got, want)
	for _, params := range []struct {
		at       time.Time
		duration time.Duration
	}{{now.Add(-time.Hour), time.Minute}, {now, time.Hour}} {
		got, err := repo.ListStaleTrips(ctx, params.at, params.duration, 100, nil)
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("different check time/duration: %+v, %v", got, err)
		}
	}
}

func TestStaleTripsBatches(t *testing.T) {
	const total, batchSize = 250, 100
	for _, equalTimes := range []bool{false, true} {
		t.Run(fmt.Sprintf("all_times_equal=%t", equalTimes), func(t *testing.T) {
			ctx := testContext(t)
			now := time.Date(2026, 11, 1, 12, 0, 0, 123456000, time.UTC)
			fixtures := make([]staleFixture, 0, total)
			for i := total - 1; i >= 0; i-- {
				at := now.Add(-2 * time.Minute)
				if !equalTimes && i >= 160 {
					at = at.Add(time.Second)
				}
				fixture := staleFixture{status: trip.StatusActive, startedAt: at}
				if i%2 == 0 {
					fixture.startedAt, fixture.lastPositionAt = now.Add(-time.Hour), &at
				}
				fixtures = append(fixtures, fixture)
			}
			want := insertStaleFixtures(t, ctx, fixtures)
			sort.Slice(want, func(i, j int) bool { return staleTripLess(want[i], want[j]) })
			repo := repository.New(testPool, queryTimeout)
			var cursor *model.StaleTripCursor
			seen := make(map[uuid.UUID]bool, total)
			for page, size := range []int{100, 100, 50} {
				batch, err := repo.ListStaleTrips(ctx, now, time.Minute, batchSize, cursor)
				if err != nil || len(batch) != size {
					t.Fatalf("batch %d: size=%d, error=%v", page+1, len(batch), err)
				}
				assertStaleTrips(t, batch, want[page*batchSize:page*batchSize+size])
				for _, found := range batch {
					if seen[found.ID] {
						t.Fatalf("duplicate trip %s", found.ID)
					}
					seen[found.ID] = true
				}
				last := batch[len(batch)-1]
				cursor = &model.StaleTripCursor{ReferenceAt: last.ReferenceAt, ID: last.ID}
			}
			end, err := repo.ListStaleTrips(ctx, now, time.Minute, batchSize, cursor)
			if err != nil || end == nil || len(end) != 0 || len(seen) != total {
				t.Fatalf("end: %+v, error=%v, unique trips=%d", end, err, len(seen))
			}
		})
	}
}

func TestStaleTripsEmptyAndInvalidArguments(t *testing.T) {
	const durationDetail = "duration"
	ctx := testContext(t)
	repo := repository.New(testPool, queryTimeout)
	now := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	empty, err := repo.ListStaleTrips(ctx, now, time.Minute, 100, nil)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty query: %+v, %v", empty, err)
	}
	for _, tt := range []struct {
		name, detail string
		size         int
		duration     time.Duration
	}{
		{"zero batch", "batch size", 0, time.Minute},
		{"negative batch", "batch size", -1, time.Minute},
		{"zero duration", durationDetail, 100, 0},
		{"negative duration", durationDetail, 100, -time.Minute},
		{"submicrosecond duration", durationDetail, 100, time.Nanosecond},
		{"fractional microsecond duration", durationDetail, 100, time.Microsecond + time.Nanosecond},
	} {
		t.Run(tt.name, func(t *testing.T) {
			found, err := repo.ListStaleTrips(ctx, now, tt.duration, tt.size, nil)
			if err == nil || !strings.Contains(err.Error(), tt.detail) || found != nil {
				t.Fatalf("invalid argument result: %+v, %v", found, err)
			}
		})
	}
}

func TestStaleTripsCancellationAndDatabaseError(t *testing.T) {
	ctx := testContext(t)
	repo := repository.New(testPool, queryTimeout)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if found, err := repo.ListStaleTrips(canceled, time.Now(), time.Minute, 100, nil); !errors.Is(err, context.Canceled) || found != nil {
		t.Fatalf("canceled query: %+v, %v", found, err)
	}
	immediate := repository.New(testPool, 0)
	if found, err := immediate.ListStaleTrips(ctx, time.Now(), time.Minute, 100, nil); !errors.Is(err, context.DeadlineExceeded) || found != nil {
		t.Fatalf("expired query timeout: %+v, %v", found, err)
	}
	tx := transaction.New(testPool, queryTimeout, slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := tx.Do(ctx, func(txCtx context.Context) error {
		actual, ok := transaction.FromContext(txCtx)
		if !ok {
			return errors.New("missing transaction")
		}
		if _, err := actual.Exec(txCtx, "SELECT 1 / 0"); err == nil {
			return errors.New("division unexpectedly succeeded")
		}
		_, err := repo.ListStaleTrips(txCtx, time.Now(), time.Minute, 100, nil)
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "25P02" {
		t.Fatalf("query in aborted transaction: %v", err)
	}
}

func TestStaleTripsUsesTransaction(t *testing.T) {
	ctx := testContext(t)
	repo := repository.New(testPool, queryTimeout)
	tx := transaction.New(testPool, queryTimeout, slog.New(slog.NewTextHandler(io.Discard, nil)))
	input := newInput(t)
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	controlErr := errors.New("roll back stale fixture")
	err := tx.Do(ctx, func(txCtx context.Context) error {
		created, err := repo.Insert(txCtx, trip.Trip{
			ID: uuid.New(), UserID: input.UserID, DriverID: input.DriverID,
			Status: trip.StatusActive, StartedAt: now.Add(-2 * time.Minute),
		})
		if err != nil {
			return err
		}
		inside, err := repo.ListStaleTrips(txCtx, now, time.Minute, 100, nil)
		if err != nil {
			return fmt.Errorf("read stale trips in transaction: %w", err)
		}
		if len(inside) != 1 || inside[0].ID != created.ID {
			return fmt.Errorf("uncommitted row missing inside transaction: %+v", inside)
		}
		outside, err := repo.ListStaleTrips(ctx, now, time.Minute, 100, nil)
		if err != nil {
			return fmt.Errorf("read stale trips outside transaction: %w", err)
		}
		if len(outside) != 0 {
			return fmt.Errorf("uncommitted row visible outside transaction: %+v", outside)
		}
		return controlErr
	})
	if !errors.Is(err, controlErr) {
		t.Fatal(err)
	}
}

func insertStaleFixtures(t *testing.T, ctx context.Context, fixtures []staleFixture) []model.StaleTrip {
	t.Helper()
	owner := staleFixtureOwner(t)
	rows := make([][]any, 0, len(fixtures))
	inserted := make([]model.StaleTrip, 0, len(fixtures))
	for _, fixture := range fixtures {
		id, driver := uuid.New(), uuid.New()
		reference := fixture.startedAt
		if fixture.lastPositionAt != nil {
			reference = *fixture.lastPositionAt
		}
		var finishedAt *time.Time
		if fixture.status == trip.StatusCompleted {
			at := fixture.startedAt.Add(time.Minute)
			finishedAt = &at
		}
		rows = append(rows, []any{id, owner, driver, 0., 0., 0., 0., int64(0), fixture.status, fixture.startedAt, finishedAt, fixture.lastPositionAt})
		inserted = append(inserted, model.StaleTrip{ID: id, DriverID: driver, ReferenceAt: reference})
	}
	if _, err := testPool.CopyFrom(ctx, pgx.Identifier{"trips"}, []string{
		"id", "user_id", "driver_id", "start_latitude", "start_longitude", "end_latitude", "end_longitude",
		"price", "status", "started_at", "finished_at", "last_position_at",
	}, pgx.CopyFromRows(rows)); err != nil {
		t.Fatal(err)
	}
	insertFixturePositions(t, ctx, owner)
	return inserted
}

func staleFixtureOwner(t *testing.T) uuid.UUID {
	t.Helper()
	owner := uuid.New()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		if _, err := testPool.Exec(ctx, "DELETE FROM trips WHERE user_id = $1", owner); err != nil {
			t.Errorf("delete stale trip fixtures: %v", err)
		}
	})
	return owner
}

func insertFixturePositions(t *testing.T, ctx context.Context, owner uuid.UUID) {
	t.Helper()
	if _, err := testPool.Exec(ctx, `INSERT INTO trip_positions (trip_id, latitude, longitude, recorded_at)
		SELECT id, 0, 0, last_position_at FROM trips WHERE user_id = $1 AND last_position_at IS NOT NULL`, owner); err != nil {
		t.Fatal(err)
	}
}

func staleTripLess(a, b model.StaleTrip) bool {
	if !a.ReferenceAt.Equal(b.ReferenceAt) {
		return a.ReferenceAt.Before(b.ReferenceAt)
	}
	return bytes.Compare(a.ID[:], b.ID[:]) < 0
}

func assertStaleTrips(t *testing.T, got, want []model.StaleTrip) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("stale trips count=%d, want %d: %+v", len(got), len(want), got)
	}
	for i, found := range got {
		if found.ID != want[i].ID || found.DriverID != want[i].DriverID || !found.ReferenceAt.Equal(want[i].ReferenceAt) {
			t.Fatalf("trip %d: %+v, want %+v", i, found, want[i])
		}
	}
}
