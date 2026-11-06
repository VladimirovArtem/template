//go:build integration

package trip_test

import (
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/infra/postgres/transaction"
	repository "github.com/VladimirovArtem/template/internal/repository/trip"
	"github.com/VladimirovArtem/template/internal/usecase/trip/create"
)

func TestCreateUsecase(t *testing.T) {
	ctx := testContext(t)
	input := newInput(t)
	id := uuid.New()
	at := time.Date(2026, 11, 1, 9, 0, 0, 123456000, time.UTC)
	repo := repository.New(testPool, queryTimeout)
	tx := transaction.New(testPool, queryTimeout, slog.New(slog.NewTextHandler(io.Discard, nil)))
	created, err := create.New(repo, tx, func() uuid.UUID { return id }, func() time.Time { return at }, testEvents).Execute(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != id || created.UserID != input.UserID || created.DriverID != input.DriverID ||
		created.StartPoint != input.StartPoint || created.EndPoint != input.EndPoint || created.Price != input.Price ||
		created.Status != trip.StatusActive || !created.StartedAt.Equal(at) || created.FinishedAt != nil {
		t.Fatalf("created trip: %+v", created)
	}
	found, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(found, created) {
		t.Fatalf("persisted trip: %+v, want %+v", found, created)
	}
	history := readHistory(t, ctx, id)
	if len(history) != 1 || history[0].FromStatus != nil || history[0].ToStatus != trip.StatusActive ||
		history[0].Reason != nil || history[0].ChangedAt.IsZero() {
		t.Fatalf("creation history: %+v", history)
	}
}

func TestDriverBusy(t *testing.T) {
	ctx := testContext(t)
	input := newInput(t)
	repo := repository.New(testPool, queryTimeout)
	tx := transaction.New(testPool, queryTimeout, slog.New(slog.NewTextHandler(io.Discard, nil)))
	usecase := create.New(repo, tx, uuid.New, time.Now, testEvents)
	created, err := usecase.Execute(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	before := readHistory(t, ctx, created.ID)
	input.UserID = uuid.New()
	if _, err := usecase.Execute(ctx, input); !errors.Is(err, trip.ErrDriverBusy) {
		t.Fatalf("create with busy driver: %v", err)
	}
	var trips, histories int
	if err := testPool.QueryRow(ctx, "SELECT count(*) FROM trips WHERE driver_id = $1", input.DriverID).Scan(&trips); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, "SELECT count(*) FROM trip_status_history h JOIN trips t ON t.id = h.trip_id WHERE t.driver_id = $1", input.DriverID).Scan(&histories); err != nil {
		t.Fatal(err)
	}
	if trips != 1 || histories != 1 || !reflect.DeepEqual(readHistory(t, ctx, created.ID), before) {
		t.Fatalf("busy driver: trips=%d, histories=%d", trips, histories)
	}
	found, err := repo.Get(ctx, created.ID)
	if err != nil || !reflect.DeepEqual(found, created) {
		t.Fatalf("original trip changed: %+v, error=%v", found, err)
	}
}
