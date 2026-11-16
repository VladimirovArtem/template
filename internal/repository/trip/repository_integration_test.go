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
	"github.com/VladimirovArtem/template/internal/usecase/trip/finish"
	"github.com/VladimirovArtem/template/internal/usecase/trip/get"
)

func TestRepositoryOutsideTransaction(t *testing.T) {
	ctx := testContext(t)
	input := newInput(t)
	repo := repository.New(testPool, queryTimeout)
	startedAt := time.Date(2026, 11, 1, 9, 0, 0, 123456000, time.UTC)
	proposed := trip.Trip{
		ID:         uuid.New(),
		UserID:     input.UserID,
		DriverID:   input.DriverID,
		StartPoint: input.StartPoint,
		EndPoint:   input.EndPoint,
		Price:      input.Price,
		Status:     trip.StatusActive,
		StartedAt:  startedAt,
	}
	created, err := repo.Insert(ctx, proposed)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != proposed.ID || created.UserID != input.UserID || created.DriverID != input.DriverID ||
		created.StartPoint != input.StartPoint || created.EndPoint != input.EndPoint || created.Price != input.Price ||
		created.Status != trip.StatusActive || !created.StartedAt.Equal(startedAt) || created.FinishedAt != nil ||
		created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("inserted trip: %+v", created)
	}
	found, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(found, created) {
		t.Fatalf("read trip = %+v, want %+v", found, created)
	}
}

func TestMissingTrip(t *testing.T) {
	ctx := testContext(t)
	id := uuid.New()
	repo := repository.New(testPool, queryTimeout)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tx := transaction.New(testPool, queryTimeout, logger)
	if _, err := get.New(repo).Execute(ctx, id); !errors.Is(err, trip.ErrNotFound) {
		t.Fatalf("get missing trip: %v", err)
	}
	if _, err := finish.New(repo, tx, time.Now, testEvents).Execute(ctx, id); !errors.Is(err, trip.ErrNotFound) {
		t.Fatalf("finish missing trip: %v", err)
	}
	if history := readHistory(t, ctx, id); len(history) != 0 {
		t.Fatalf("missing trip history: %+v", history)
	}
}
