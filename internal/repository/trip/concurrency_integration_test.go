//go:build integration

package trip_test

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/infra/postgres/transaction"
	repository "github.com/VladimirovArtem/template/internal/repository/trip"
	"github.com/VladimirovArtem/template/internal/usecase/trip/create"
	"github.com/VladimirovArtem/template/internal/usecase/trip/finish"
)

func TestConcurrentCreate(t *testing.T) {
	const attempts = 6
	ctx := testContext(t)
	input := newInput(t)
	repo := repository.New(testPool, queryTimeout)
	tx := transaction.New(testPool, queryTimeout, slog.New(slog.NewTextHandler(io.Discard, nil)))
	creator := create.New(repo, tx, uuid.New, time.Now, testEvents)
	type result struct {
		tripData trip.Trip
		err      error
	}
	results := make(chan result, attempts)
	start := make(chan struct{})
	var ready, workers sync.WaitGroup
	ready.Add(attempts)
	workers.Add(attempts)
	for range attempts {
		go func() {
			defer workers.Done()
			ready.Done()
			select {
			case <-start:
			case <-ctx.Done():
				results <- result{err: ctx.Err()}
				return
			}
			created, err := creator.Execute(ctx, input)
			results <- result{tripData: created, err: err}
		}()
	}
	ready.Wait()
	close(start)
	workers.Wait()
	close(results)
	var successes, busy int
	var winner trip.Trip
	for result := range results {
		switch {
		case result.err == nil:
			successes++
			winner = result.tripData
		case errors.Is(result.err, trip.ErrDriverBusy):
			busy++
		default:
			t.Fatalf("concurrent create: %v", result.err)
		}
	}
	if successes != 1 || busy != attempts-1 {
		t.Fatalf("concurrent create: successes=%d, ErrDriverBusy=%d", successes, busy)
	}
	var trips, histories int
	if err := testPool.QueryRow(ctx, "SELECT count(*) FROM trips WHERE driver_id = $1", input.DriverID).Scan(&trips); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, "SELECT count(*) FROM trip_status_history h JOIN trips t ON t.id = h.trip_id WHERE t.driver_id = $1", input.DriverID).Scan(&histories); err != nil {
		t.Fatal(err)
	}
	found, err := repo.Get(ctx, winner.ID)
	if err != nil {
		t.Fatal(err)
	}
	history := readHistory(t, ctx, winner.ID)
	if trips != 1 || histories != 1 || found.DriverID != input.DriverID || found.Status != trip.StatusActive ||
		len(history) != 1 || history[0].FromStatus != nil || history[0].ToStatus != trip.StatusActive {
		t.Fatalf("concurrent create persisted trips=%d, histories=%d, trip=%+v, history=%+v", trips, histories, found, history)
	}
	t.Logf("concurrent create: successes=%d, ErrDriverBusy=%d, trips=%d, history=%d", successes, busy, trips, histories)
}

func TestConcurrentFinish(t *testing.T) {
	const attempts = 6
	ctx := testContext(t)
	input := newInput(t)
	repo := repository.New(testPool, queryTimeout)
	tx := transaction.New(testPool, queryTimeout, slog.New(slog.NewTextHandler(io.Discard, nil)))
	startedAt := time.Date(2026, 11, 1, 9, 0, 0, 123456000, time.UTC)
	created, err := create.New(repo, tx, uuid.New, func() time.Time { return startedAt }, testEvents).Execute(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		tripData trip.Trip
		at       time.Time
		err      error
	}
	results := make(chan result, attempts)
	start := make(chan struct{})
	var ready, workers sync.WaitGroup
	ready.Add(attempts)
	workers.Add(attempts)
	for i := range attempts {
		go func() {
			defer workers.Done()
			at := startedAt.Add(time.Hour + time.Duration(i)*time.Microsecond)
			finisher := finish.New(repo, tx, func() time.Time { return at }, testEvents)
			ready.Done()
			select {
			case <-start:
			case <-ctx.Done():
				results <- result{err: ctx.Err()}
				return
			}
			finished, err := finisher.Execute(ctx, created.ID)
			results <- result{tripData: finished, at: at, err: err}
		}()
	}
	ready.Wait()
	close(start)
	workers.Wait()
	close(results)
	var successes, completed int
	var winner result
	for result := range results {
		switch {
		case result.err == nil:
			successes++
			winner = result
		case errors.Is(result.err, trip.ErrCompleted):
			completed++
		default:
			t.Fatalf("concurrent finish: %v", result.err)
		}
	}
	if successes != 1 || completed != attempts-1 {
		t.Fatalf("concurrent finish: successes=%d, ErrCompleted=%d", successes, completed)
	}
	found, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if winner.tripData.Status != trip.StatusCompleted || winner.tripData.FinishedAt == nil || !winner.tripData.FinishedAt.Equal(winner.at) ||
		found.Status != trip.StatusCompleted || found.FinishedAt == nil || !found.FinishedAt.Equal(winner.at) || !found.UpdatedAt.Equal(winner.at) {
		t.Fatalf("completion time: winner=%+v, proposed=%s, persisted=%+v", winner.tripData, winner.at, found)
	}
	history := readHistory(t, ctx, created.ID)
	if len(history) != 2 || history[0].FromStatus != nil || history[0].ToStatus != trip.StatusActive ||
		history[1].FromStatus == nil || *history[1].FromStatus != trip.StatusActive || history[1].ToStatus != trip.StatusCompleted {
		t.Fatalf("concurrent finish history: %+v", history)
	}
	t.Logf("concurrent finish: successes=%d, ErrCompleted=%d, completion history=1, finished_at=%s", successes, completed, found.FinishedAt.Format(time.RFC3339Nano))
}
