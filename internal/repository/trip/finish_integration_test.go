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
	"github.com/VladimirovArtem/template/internal/usecase/trip/finish"
)

func TestFinishAndRepeatedFinish(t *testing.T) {
	ctx := testContext(t)
	input := newInput(t)
	repo := repository.New(testPool, queryTimeout)
	tx := transaction.New(testPool, queryTimeout, slog.New(slog.NewTextHandler(io.Discard, nil)))
	startedAt := time.Date(2026, 11, 1, 9, 0, 0, 123456000, time.UTC)
	created, err := create.New(repo, tx, uuid.New, func() time.Time { return startedAt }, testEvents).Execute(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	at := startedAt.Add(time.Hour)
	finished, err := finish.New(repo, tx, func() time.Time { return at }, testEvents).Execute(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != trip.StatusCompleted || finished.FinishedAt == nil || !finished.FinishedAt.Equal(at) || !finished.UpdatedAt.Equal(at) {
		t.Fatalf("finished trip: %+v", finished)
	}
	want := created
	want.Status, want.FinishedAt, want.UpdatedAt = trip.StatusCompleted, &at, at
	if finished.ID != want.ID || finished.UserID != want.UserID || finished.DriverID != want.DriverID ||
		finished.StartPoint != want.StartPoint || finished.EndPoint != want.EndPoint || finished.Price != want.Price ||
		!finished.StartedAt.Equal(want.StartedAt) || !finished.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("completion changed other fields: %+v", finished)
	}
	before := readHistory(t, ctx, created.ID)
	if len(before) != 2 || before[0].FromStatus != nil || before[0].ToStatus != trip.StatusActive ||
		before[1].FromStatus == nil || *before[1].FromStatus != trip.StatusActive || before[1].ToStatus != trip.StatusCompleted || before[1].Reason != nil {
		t.Fatalf("completion history: %+v", before)
	}
	if _, err := finish.New(repo, tx, func() time.Time { return at.Add(time.Hour) }, testEvents).Execute(ctx, created.ID); !errors.Is(err, trip.ErrCompleted) {
		t.Fatalf("repeat completion: %v", err)
	}
	found, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(found, finished) || !reflect.DeepEqual(readHistory(t, ctx, created.ID), before) {
		t.Fatalf("repeated completion changed trip or history: %+v", found)
	}
}

func TestDriverAvailableAfterFinish(t *testing.T) {
	ctx := testContext(t)
	input := newInput(t)
	repo := repository.New(testPool, queryTimeout)
	tx := transaction.New(testPool, queryTimeout, slog.New(slog.NewTextHandler(io.Discard, nil)))
	creator := create.New(repo, tx, uuid.New, time.Now, testEvents)
	first, err := creator.Execute(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := finish.New(repo, tx, time.Now, testEvents).Execute(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	input.UserID = uuid.New()
	second, err := creator.Execute(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID || second.DriverID != input.DriverID || second.Status != trip.StatusActive {
		t.Fatalf("new active trip: %+v", second)
	}
	var active int
	if err := testPool.QueryRow(ctx, "SELECT count(*) FROM trips WHERE driver_id = $1 AND status = $2", input.DriverID, trip.StatusActive).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 1 || len(readHistory(t, ctx, first.ID)) != 2 || len(readHistory(t, ctx, second.ID)) != 1 {
		t.Fatalf("driver reuse: active trips=%d", active)
	}
}
