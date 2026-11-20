//go:build integration

package trip_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/infra/postgres/transaction"
	"github.com/VladimirovArtem/template/internal/model"
	"github.com/VladimirovArtem/template/internal/usecase/trip/finish"
)

func TestPositionConcurrentFinish(t *testing.T) {
	for _, finishFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("finish_first=%t", finishFirst), func(t *testing.T) {
			ctx := testContext(t)
			repo, tx, created := positionFixture(t)
			at := time.Now().UTC().Truncate(time.Microsecond)
			saver := newPositionSaver(t, repo, tx, func() time.Time { return at })
			finisher := finish.New(repo, tx, func() time.Time { return at.Add(time.Second) }, testEvents)
			saveOperation := func(txCtx context.Context) error {
				_, err := saver.Execute(txCtx, model.CreatePositionInput{TripID: created.ID, RecordedAt: at})
				return err
			}
			var finished trip.Trip
			finishOperation := func(txCtx context.Context) error {
				var err error
				finished, err = finisher.Execute(txCtx, created.ID)
				return err
			}
			firstOperation, secondOperation := saveOperation, finishOperation
			if finishFirst {
				firstOperation, secondOperation = secondOperation, firstOperation
			}
			firstReady, secondReady := make(chan int32, 1), make(chan int32, 1)
			firstDone, secondDone := make(chan error, 1), make(chan error, 1)
			release := make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			defer unblock()
			go func() {
				err := tx.Do(ctx, func(txCtx context.Context) error {
					pid, err := transactionPID(txCtx)
					if err != nil {
						return err
					}
					if err := firstOperation(txCtx); err != nil {
						return err
					}
					firstReady <- pid
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				})
				firstDone <- err
			}()
			blockerPID := waitPositionPID(t, ctx, firstReady, firstDone)
			go func() {
				err := tx.Do(ctx, func(txCtx context.Context) error {
					pid, err := transactionPID(txCtx)
					if err != nil {
						return err
					}
					secondReady <- pid
					return secondOperation(txCtx)
				})
				secondDone <- err
			}()
			blockedPID := waitPositionPID(t, ctx, secondReady, secondDone)
			waitForTransactionBlock(t, ctx, blockedPID, blockerPID)
			unblock()
			one, two := receivePositionRace(t, ctx, firstDone), receivePositionRace(t, ctx, secondDone)
			if one != nil {
				t.Fatalf("first operation: %v", one)
			}
			var wantErr error
			if finishFirst {
				wantErr = trip.ErrCompleted
			}
			if !errors.Is(two, wantErr) {
				t.Fatalf("second operation: %v, want %v", two, wantErr)
			}
			found, err := repo.Get(ctx, created.ID)
			if err != nil || found.Status != trip.StatusCompleted || len(readHistory(t, ctx, created.ID)) != 2 {
				t.Fatalf("completed state: %+v, %v", found, err)
			}
			points, err := repo.ListPositions(ctx, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			if finishFirst {
				if len(points) != 0 || found.LastPositionAt != nil || finished.LastPositionAt != nil {
					t.Fatalf("position inserted after finish: trip=%+v, positions=%+v", found, points)
				}
			} else if len(points) != 1 || found.LastPositionAt == nil || !found.LastPositionAt.Equal(at) ||
				finished.LastPositionAt == nil || !finished.LastPositionAt.Equal(at) || !points[0].RecordedAt.Equal(at) {
				t.Fatalf("position lost during finish: trip=%+v, finish=%+v, positions=%+v", found, finished, points)
			}
			t.Logf("finish_first=%t: PostgreSQL confirmed lock wait, positions=%d, last_position_at=%v", finishFirst, len(points), found.LastPositionAt)
		})
	}
}

func transactionPID(ctx context.Context) (int32, error) {
	tx, ok := transaction.FromContext(ctx)
	if !ok {
		return 0, errors.New("transaction missing from context")
	}
	var pid int32
	err := tx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid)
	return pid, err
}

func waitForTransactionBlock(t *testing.T, ctx context.Context, blockedPID, blockerPID int32) {
	t.Helper()
	// Synchronize on PostgreSQL's actual lock graph, not on elapsed time.
	for {
		var blocked bool
		if err := testPool.QueryRow(ctx, "SELECT $2 = ANY(pg_blocking_pids($1))", blockedPID, blockerPID).Scan(&blocked); err != nil {
			t.Fatalf("wait for PostgreSQL row lock: %v", err)
		}
		if blocked {
			return
		}
	}
}

func waitPositionPID(t *testing.T, ctx context.Context, ready <-chan int32, done <-chan error) int32 {
	t.Helper()
	select {
	case pid := <-ready:
		return pid
	case err := <-done:
		t.Fatalf("operation ended before lock synchronization: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	return 0
}

func receivePositionRace(t *testing.T, ctx context.Context, done <-chan error) error {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return ctx.Err()
	}
}

func TestConcurrentPositionsKeepMaximumTime(t *testing.T) {
	const attempts = 6
	ctx := testContext(t)
	repo, tx, created := positionFixture(t)
	at := time.Now().UTC().Truncate(time.Microsecond)
	saver := newPositionSaver(t, repo, tx, func() time.Time { return at })
	start, done := make(chan struct{}), make(chan error, attempts)
	var ready sync.WaitGroup
	ready.Add(attempts)
	for i := range attempts {
		go func() {
			ready.Done()
			select {
			case <-start:
			case <-ctx.Done():
				done <- ctx.Err()
				return
			}
			_, err := saver.Execute(ctx, model.CreatePositionInput{TripID: created.ID, RecordedAt: at.Add(-time.Duration(i) * time.Second)})
			done <- err
		}()
	}
	ready.Wait()
	close(start)
	for range attempts {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	found, err := repo.Get(ctx, created.ID)
	if err != nil || found.LastPositionAt == nil || !found.LastPositionAt.Equal(at) {
		t.Fatalf("concurrent maximum: %+v, %v", found, err)
	}
	points, err := repo.ListPositions(ctx, created.ID)
	if err != nil || len(points) != attempts {
		t.Fatalf("concurrent positions: %+v, %v", points, err)
	}
	for i, point := range points {
		want := at.Add(-time.Duration(attempts-1-i) * time.Second)
		if !point.RecordedAt.Equal(want) {
			t.Fatalf("point %d: %s, want %s", i, point.RecordedAt, want)
		}
	}
}
