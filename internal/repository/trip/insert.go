package trip

import (
	"context"
	"errors"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/VladimirovArtem/template/internal/domain/trip"
)

func (r *Repository) Insert(ctx context.Context, tripData trip.Trip) (trip.Trip, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	query, args, err := sq.Insert("trips").Columns(
		"id", "user_id", "driver_id", "start_latitude", "start_longitude", "end_latitude", "end_longitude",
		"price", "status", "started_at", "finished_at",
	).Values(tripData.ID, tripData.UserID, tripData.DriverID, tripData.StartPoint.Latitude, tripData.StartPoint.Longitude,
		tripData.EndPoint.Latitude, tripData.EndPoint.Longitude, tripData.Price, tripData.Status, tripData.StartedAt, tripData.FinishedAt).
		Suffix("RETURNING " + tripColumns).PlaceholderFormat(sq.Dollar).ToSql()
	if err != nil {
		return trip.Trip{}, fmt.Errorf("build trip insert: %w", err)
	}
	created, err := scanTrip(executorFor(ctx, r.pool).QueryRow(ctx, query, args...))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode && pgErr.ConstraintName == activeDriverIndex {
			err = errors.Join(trip.ErrDriverBusy, err)
		}
		return trip.Trip{}, fmt.Errorf("insert trip: %w", err)
	}
	return created, nil
}
