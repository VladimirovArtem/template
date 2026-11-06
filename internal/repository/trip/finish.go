package trip

import (
	"context"
	"errors"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/VladimirovArtem/template/internal/domain/trip"
)

func (r *Repository) Finish(ctx context.Context, tripData trip.Trip) (trip.Trip, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	query, args, err := sq.Update("trips").Set("status", tripData.Status).
		Set("finished_at", tripData.FinishedAt).Set("updated_at", tripData.UpdatedAt).
		Where(sq.Eq{"id": tripData.ID, "status": trip.StatusActive}).Suffix("RETURNING " + tripColumns).
		PlaceholderFormat(sq.Dollar).ToSql()
	if err != nil {
		return trip.Trip{}, fmt.Errorf("build trip update: %w", err)
	}
	finishedTrip, err := scanTrip(executorFor(ctx, r.pool).QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		current, err := r.Get(ctx, tripData.ID)
		if err != nil {
			return trip.Trip{}, fmt.Errorf("check trip after completion conflict: %w", err)
		}
		if current.Status == trip.StatusCompleted {
			return trip.Trip{}, trip.ErrCompleted
		}
		return trip.Trip{}, errors.New("trip state changed unexpectedly during completion")
	}
	if err != nil {
		return trip.Trip{}, fmt.Errorf("update trip: %w", err)
	}
	return finishedTrip, nil
}
