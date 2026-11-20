//go:build integration

package trip_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/infra/postgres/transaction"
	"github.com/VladimirovArtem/template/internal/model"
	repository "github.com/VladimirovArtem/template/internal/repository/trip"
	positionlist "github.com/VladimirovArtem/template/internal/usecase/position/list"
	"github.com/VladimirovArtem/template/internal/usecase/trip/create"
	"github.com/VladimirovArtem/template/internal/usecase/trip/finish"
)

func positionFixture(t *testing.T) (*repository.Repository, *transaction.Manager, trip.Trip) {
	t.Helper()
	repo := repository.New(testPool, queryTimeout)
	tx := transaction.New(testPool, queryTimeout, slog.New(slog.NewTextHandler(io.Discard, nil)))
	created, err := create.New(repo, tx, uuid.New, time.Now, testEvents).Execute(testContext(t), newInput(t))
	if err != nil {
		t.Fatal(err)
	}
	if created.LastPositionAt != nil {
		t.Fatalf("new trip last position: %v", created.LastPositionAt)
	}
	return repo, tx, created
}

func TestPositionsRouteAndLastTime(t *testing.T) {
	ctx := testContext(t)
	repo, tx, created := positionFixture(t)
	lister := positionlist.New(repo)
	points, err := lister.Execute(ctx, created.ID)
	if err != nil || points == nil || len(points) != 0 {
		t.Fatalf("empty existing route: %+v, %v", points, err)
	}
	now := time.Date(2026, 11, 1, 9, 0, 0, 123456000, time.UTC)
	saver := newPositionSaver(t, repo, tx, func() time.Time { return now })
	inserted := make([]trip.Position, 0, 4)
	for i, at := range []time.Time{now, now.Add(-time.Hour), now, now.Add(-time.Minute)} {
		input := model.CreatePositionInput{TripID: created.ID, Coordinates: trip.Coordinates{Latitude: float64(i), Longitude: -float64(i)}, RecordedAt: at}
		point, err := saver.Execute(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		if point.ID <= 0 || point.TripID != created.ID || point.Coordinates != input.Coordinates || !point.RecordedAt.Equal(at) || point.CreatedAt.IsZero() {
			t.Fatalf("saved point: %+v", point)
		}
		inserted = append(inserted, point)
		found, err := repo.Get(ctx, created.ID)
		if err != nil || found.LastPositionAt == nil || !found.LastPositionAt.Equal(now) {
			t.Fatalf("last position after point %d: %+v, %v", i, found, err)
		}
	}
	want := []trip.Position{inserted[1], inserted[3], inserted[0], inserted[2]}
	points, err = lister.Execute(ctx, created.ID)
	if err != nil || !reflect.DeepEqual(points, want) {
		t.Fatalf("sorted route = %+v, %v; want %+v", points, err, want)
	}
	finished, err := finish.New(repo, tx, time.Now, testEvents).Execute(ctx, created.ID)
	if err != nil || finished.LastPositionAt == nil || !finished.LastPositionAt.Equal(now) {
		t.Fatalf("finished trip: %+v, %v", finished, err)
	}
	points, err = lister.Execute(ctx, created.ID)
	if err != nil || !reflect.DeepEqual(points, want) {
		t.Fatalf("completed route = %+v, %v", points, err)
	}
	if _, err := saver.Execute(ctx, model.CreatePositionInput{TripID: created.ID, RecordedAt: now.Add(time.Second)}); !errors.Is(err, trip.ErrCompleted) {
		t.Fatalf("save to completed trip: %v", err)
	}
	after, err := repo.Get(ctx, created.ID)
	if err != nil || !reflect.DeepEqual(after, finished) {
		t.Fatalf("rejected save changed completed trip: %+v, %v", after, err)
	}
	points, err = lister.Execute(ctx, created.ID)
	if err != nil || !reflect.DeepEqual(points, want) {
		t.Fatalf("rejected save changed route: %+v, %v", points, err)
	}
}

func TestPositionMissingTrip(t *testing.T) {
	ctx := testContext(t)
	repo := repository.New(testPool, queryTimeout)
	tx := transaction.New(testPool, queryTimeout, slog.New(slog.NewTextHandler(io.Discard, nil)))
	id := uuid.New()
	if _, err := newPositionSaver(t, repo, tx, time.Now).Execute(ctx, model.CreatePositionInput{TripID: id, RecordedAt: time.Now().UTC()}); !errors.Is(err, trip.ErrNotFound) {
		t.Fatalf("save for missing trip: %v", err)
	}
	if _, err := positionlist.New(repo).Execute(ctx, id); !errors.Is(err, trip.ErrNotFound) {
		t.Fatalf("read missing route: %v", err)
	}
}

func TestPositionRollback(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing=%t", existing), func(t *testing.T) {
			ctx := testContext(t)
			repo, tx, created := positionFixture(t)
			now := time.Date(2026, 11, 1, 9, 0, 0, 123456000, time.UTC)
			saver := newPositionSaver(t, repo, tx, func() time.Time { return now })
			if existing {
				if _, err := saver.Execute(ctx, model.CreatePositionInput{TripID: created.ID, RecordedAt: now.Add(-time.Hour)}); err != nil {
					t.Fatal(err)
				}
			}
			before, err := repo.Get(ctx, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			pointsBefore, err := repo.ListPositions(ctx, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			controlErr := errors.New("force rollback after both writes")
			err = tx.Do(ctx, func(txCtx context.Context) error {
				if _, err := saver.Execute(txCtx, model.CreatePositionInput{TripID: created.ID, RecordedAt: now}); err != nil {
					return err
				}
				inside, err := repo.Get(txCtx, created.ID)
				if err != nil {
					return err
				}
				points, err := repo.ListPositions(txCtx, created.ID)
				if err != nil {
					return err
				}
				if inside.LastPositionAt == nil || !inside.LastPositionAt.Equal(now) || len(points) != len(pointsBefore)+1 {
					return fmt.Errorf("writes missing inside transaction: trip=%+v, positions=%+v", inside, points)
				}
				return controlErr
			})
			if !errors.Is(err, controlErr) {
				t.Fatalf("rollback error = %v", err)
			}
			after, err := repo.Get(ctx, created.ID)
			if err != nil || !reflect.DeepEqual(after, before) {
				t.Fatalf("trip after rollback: %+v, %v; want %+v", after, err, before)
			}
			pointsAfter, err := repo.ListPositions(ctx, created.ID)
			if err != nil || !reflect.DeepEqual(pointsAfter, pointsBefore) {
				t.Fatalf("positions after rollback: %+v, %v", pointsAfter, err)
			}
		})
	}
}

func TestPositionInsertFailureRollsBackTime(t *testing.T) {
	ctx := testContext(t)
	repo, tx, created := positionFixture(t)
	for _, coordinates := range []trip.Coordinates{{Latitude: 91}, {Longitude: 181}} {
		err := tx.Do(ctx, func(txCtx context.Context) error {
			at := time.Now().UTC()
			if err := repo.UpdateLastPositionAt(txCtx, created.ID, at); err != nil {
				return err
			}
			_, err := repo.InsertPosition(txCtx, trip.Position{TripID: created.ID, Coordinates: coordinates, RecordedAt: at})
			return err
		})
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Fatalf("position CHECK constraint: %v", err)
		}
		found, err := repo.Get(ctx, created.ID)
		if err != nil || !reflect.DeepEqual(found, created) {
			t.Fatalf("failed insert changed trip: %+v, %v", found, err)
		}
		points, err := repo.ListPositions(ctx, created.ID)
		if err != nil || len(points) != 0 {
			t.Fatalf("failed insert left points: %+v, %v", points, err)
		}
	}
}

func TestPositionDeleteCascade(t *testing.T) {
	ctx := testContext(t)
	repo, tx, created := positionFixture(t)
	if _, err := newPositionSaver(t, repo, tx, time.Now).Execute(ctx, model.CreatePositionInput{TripID: created.ID, RecordedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, "DELETE FROM trips WHERE id = $1", created.ID); err != nil {
		t.Fatal(err)
	}
	points, err := repo.ListPositions(ctx, created.ID)
	if err != nil || len(points) != 0 || len(readHistory(t, ctx, created.ID)) != 0 {
		t.Fatalf("cascade cleanup: positions=%+v, error=%v", points, err)
	}
}
