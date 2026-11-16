package trip

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/infra/postgres/transaction"
)

const (
	activeDriverIndex   = "trips_one_active_per_driver_idx"
	uniqueViolationCode = "23505"
	tripColumns         = "id, user_id, driver_id, start_latitude, start_longitude, end_latitude, end_longitude, price, status, started_at, finished_at, created_at, updated_at"
)

type Repository struct {
	pool    *pgxpool.Pool
	timeout time.Duration
}

func New(pool *pgxpool.Pool, queryTimeout time.Duration) *Repository {
	return &Repository{pool: pool, timeout: queryTimeout}
}

func scanTrip(row pgx.Row) (trip.Trip, error) {
	var tripData trip.Trip
	err := row.Scan(&tripData.ID, &tripData.UserID, &tripData.DriverID,
		&tripData.StartPoint.Latitude, &tripData.StartPoint.Longitude, &tripData.EndPoint.Latitude, &tripData.EndPoint.Longitude,
		&tripData.Price, &tripData.Status, &tripData.StartedAt, &tripData.FinishedAt, &tripData.CreatedAt, &tripData.UpdatedAt)
	return tripData, err
}

func executorFor(ctx context.Context, pool *pgxpool.Pool) executor {
	if tx, ok := transaction.FromContext(ctx); ok {
		return tx
	}
	return pool
}
