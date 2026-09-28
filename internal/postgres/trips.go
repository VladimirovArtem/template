package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/VladimirovArtem/template/internal/trip"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const activeDriverIndex = "trips_one_active_per_driver_idx"
const tripColumns = "id, user_id, driver_id, start_latitude, start_longitude, end_latitude, end_longitude, price, status, started_at, finished_at, created_at, updated_at"

type Trips struct {
	pool    *pgxpool.Pool
	timeout time.Duration
}

var _ trip.Repository = (*Trips)(nil)

func NewTrips(pool *pgxpool.Pool, queryTimeout time.Duration) *Trips {
	return &Trips{pool: pool, timeout: queryTimeout}
}

func (r *Trips) Insert(ctx context.Context, tripData trip.Trip) (trip.Trip, error) {
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
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == activeDriverIndex {
			err = errors.Join(trip.ErrDriverBusy, err)
		}
		return trip.Trip{}, fmt.Errorf("insert trip: %w", err)
	}
	return created, nil
}

func (r *Trips) Get(ctx context.Context, id uuid.UUID) (trip.Trip, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	query, args, err := sq.Select(strings.Split(tripColumns, ", ")...).From("trips").Where(sq.Eq{"id": id}).PlaceholderFormat(sq.Dollar).ToSql()
	if err != nil {
		return trip.Trip{}, fmt.Errorf("build trip select: %w", err)
	}
	foundTrip, err := scanTrip(executorFor(ctx, r.pool).QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return trip.Trip{}, trip.ErrNotFound
	}
	if err != nil {
		return trip.Trip{}, fmt.Errorf("select trip: %w", err)
	}
	return foundTrip, nil
}

func (r *Trips) Finish(ctx context.Context, id uuid.UUID, finishedAt time.Time) (trip.Trip, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	query, args, err := sq.Update("trips").Set("status", trip.Completed).
		Set("finished_at", finishedAt).Set("updated_at", finishedAt).
		Where(sq.Eq{"id": id, "status": trip.Active}).Suffix("RETURNING " + tripColumns).
		PlaceholderFormat(sq.Dollar).ToSql()
	if err != nil {
		return trip.Trip{}, fmt.Errorf("build trip update: %w", err)
	}
	finishedTrip, err := scanTrip(executorFor(ctx, r.pool).QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		current, err := r.Get(ctx, id)
		if err != nil {
			return trip.Trip{}, err
		}
		if current.Status == trip.Completed {
			return trip.Trip{}, trip.ErrCompleted
		}
		return trip.Trip{}, fmt.Errorf("trip state changed unexpectedly during completion")
	}
	if err != nil {
		return trip.Trip{}, fmt.Errorf("update trip: %w", err)
	}
	return finishedTrip, nil
}

func (r *Trips) AddHistory(ctx context.Context, entry trip.History) error {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	query, args, err := sq.Insert("trip_status_history").Columns("trip_id", "from_status", "to_status", "reason").
		Values(entry.TripID, entry.FromStatus, entry.ToStatus, entry.Reason).PlaceholderFormat(sq.Dollar).ToSql()
	if err != nil {
		return fmt.Errorf("build history insert: %w", err)
	}
	if _, err := executorFor(ctx, r.pool).Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("insert trip history: %w", err)
	}
	return nil
}

func scanTrip(row pgx.Row) (trip.Trip, error) {
	var tripData trip.Trip
	err := row.Scan(&tripData.ID, &tripData.UserID, &tripData.DriverID,
		&tripData.StartPoint.Latitude, &tripData.StartPoint.Longitude, &tripData.EndPoint.Latitude, &tripData.EndPoint.Longitude,
		&tripData.Price, &tripData.Status, &tripData.StartedAt, &tripData.FinishedAt, &tripData.CreatedAt, &tripData.UpdatedAt)
	return tripData, err
}
