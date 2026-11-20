package trip

import (
	"context"
	"errors"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/VladimirovArtem/template/internal/model"
)

const staleTripTime = "COALESCE(last_position_at, started_at)"

func (r *Repository) StillStale(ctx context.Context, candidate model.StaleTrip) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	const query = "SELECT EXISTS (SELECT 1 FROM trips WHERE id = $1 AND status = 'active' AND " + staleTripTime + " = $2)"
	var current bool
	err := executorFor(ctx, r.pool).QueryRow(ctx, query, candidate.ID, candidate.ReferenceAt).Scan(&current)
	if err != nil {
		return false, fmt.Errorf("recheck stale trip: %w", err)
	}
	return current, nil
}

func (r *Repository) ListStaleTrips(ctx context.Context, checkedAt time.Time, staleAfter time.Duration, batchSize int, after *model.StaleTripCursor) ([]model.StaleTrip, error) {
	if batchSize <= 0 {
		return nil, errors.New("stale trip batch size must be positive")
	}
	if staleAfter <= 0 || staleAfter%time.Microsecond != 0 {
		return nil, errors.New("stale trip duration must be positive and a whole number of microseconds")
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	query := sq.Select("id", "driver_id", staleTripTime).From("trips").
		Where("status = 'active'").
		Where(staleTripTime+" < ?::timestamptz - (?::bigint * INTERVAL '1 microsecond')", checkedAt, staleAfter.Microseconds()).
		OrderBy(staleTripTime, "id")
	if after != nil {
		query = query.Where("("+staleTripTime+", id) > (?, ?)", after.ReferenceAt, after.ID)
	}
	sql, args, err := query.Suffix("LIMIT ?", batchSize).PlaceholderFormat(sq.Dollar).ToSql()
	if err != nil {
		return nil, fmt.Errorf("build stale trips select: %w", err)
	}
	rows, err := executorFor(ctx, r.pool).Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("select stale trips: %w", err)
	}
	defer rows.Close()
	trips := make([]model.StaleTrip, 0)
	for rows.Next() {
		var found model.StaleTrip
		if err := rows.Scan(&found.ID, &found.DriverID, &found.ReferenceAt); err != nil {
			return nil, fmt.Errorf("scan stale trip: %w", err)
		}
		trips = append(trips, found)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read stale trips: %w", err)
	}
	return trips, nil
}
