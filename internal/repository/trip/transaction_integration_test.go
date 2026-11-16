//go:build integration

package trip_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/infra/postgres/transaction"
	"github.com/VladimirovArtem/template/internal/model"
	repository "github.com/VladimirovArtem/template/internal/repository/trip"
)

func TestRollbackTripAndHistory(t *testing.T) {
	ctx := testContext(t)
	input := newInput(t)
	id := uuid.New()
	repo := repository.New(testPool, queryTimeout)
	tx := transaction.New(testPool, queryTimeout, slog.New(slog.NewTextHandler(io.Discard, nil)))
	controlErr := errors.New("force transaction rollback")
	err := tx.Do(ctx, func(txCtx context.Context) error {
		if _, err := repo.Insert(txCtx, trip.Trip{
			ID:         id,
			UserID:     input.UserID,
			DriverID:   input.DriverID,
			StartPoint: input.StartPoint,
			EndPoint:   input.EndPoint,
			Price:      input.Price,
			Status:     trip.StatusActive,
			StartedAt:  time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("insert trip before rollback: %w", err)
		}
		if err := repo.AddHistory(txCtx, model.TripHistory{TripID: id, ToStatus: trip.StatusActive}); err != nil {
			return fmt.Errorf("insert history before rollback: %w", err)
		}
		actualTx, exists := transaction.FromContext(txCtx)
		if !exists {
			return errors.New("transaction missing from callback context")
		}
		var trips, histories int
		if err := actualTx.QueryRow(txCtx, "SELECT (SELECT count(*) FROM trips WHERE id = $1), (SELECT count(*) FROM trip_status_history WHERE trip_id = $1)", id).Scan(&trips, &histories); err != nil {
			return fmt.Errorf("verify inserts inside transaction: %w", err)
		}
		if trips != 1 || histories != 1 {
			return fmt.Errorf("before rollback: trips=%d, histories=%d", trips, histories)
		}
		return controlErr
	})
	if !errors.Is(err, controlErr) {
		t.Fatalf("Do() = %v, want control error", err)
	}
	var trips, histories int
	if err := testPool.QueryRow(ctx, "SELECT count(*) FROM trips WHERE id = $1", id).Scan(&trips); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, "SELECT count(*) FROM trip_status_history WHERE trip_id = $1", id).Scan(&histories); err != nil {
		t.Fatal(err)
	}
	if trips != 0 || histories != 0 {
		t.Fatalf("after rollback: trips=%d, histories=%d", trips, histories)
	}
	t.Log("rollback: both rows existed inside Do; outside transaction trips=0, history=0")
}
