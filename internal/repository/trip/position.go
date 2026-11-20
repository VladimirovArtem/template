package trip

import (
	"context"
	"errors"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/VladimirovArtem/template/internal/domain/trip"
)

const positionColumns = "id, trip_id, latitude, longitude, recorded_at, created_at"

func (r *Repository) UpdateLastPositionAt(ctx context.Context, id uuid.UUID, recordedAt time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	query, args, err := sq.Update("trips").
		Set("last_position_at", sq.Expr("GREATEST(last_position_at, ?)", recordedAt)).
		Set("updated_at", sq.Expr("now()")).
		Where(sq.Eq{"id": id, "status": trip.StatusActive}).PlaceholderFormat(sq.Dollar).ToSql()
	if err != nil {
		return fmt.Errorf("build last position update: %w", err)
	}
	result, err := executorFor(ctx, r.pool).Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update last position: %w", err)
	}
	if result.RowsAffected() != 0 {
		return nil
	}
	current, err := r.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("check trip after position conflict: %w", err)
	}
	if current.Status == trip.StatusCompleted {
		return trip.ErrCompleted
	}
	return errors.New("trip state changed unexpectedly during position update")
}

func (r *Repository) InsertPosition(ctx context.Context, position trip.Position) (trip.Position, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	query, args, err := sq.Insert("trip_positions").Columns("trip_id", "latitude", "longitude", "recorded_at").
		Values(position.TripID, position.Coordinates.Latitude, position.Coordinates.Longitude, position.RecordedAt).
		Suffix("RETURNING " + positionColumns).PlaceholderFormat(sq.Dollar).ToSql()
	if err != nil {
		return trip.Position{}, fmt.Errorf("build position insert: %w", err)
	}
	created, err := scanPosition(executorFor(ctx, r.pool).QueryRow(ctx, query, args...))
	if err != nil {
		return trip.Position{}, fmt.Errorf("insert position: %w", err)
	}
	return created, nil
}

func (r *Repository) ListPositions(ctx context.Context, id uuid.UUID) ([]trip.Position, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	query, args, err := sq.Select(positionColumns).From("trip_positions").Where(sq.Eq{"trip_id": id}).
		OrderBy("recorded_at", "id").PlaceholderFormat(sq.Dollar).ToSql()
	if err != nil {
		return nil, fmt.Errorf("build positions select: %w", err)
	}
	rows, err := executorFor(ctx, r.pool).Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("select positions: %w", err)
	}
	defer rows.Close()
	positions := make([]trip.Position, 0)
	for rows.Next() {
		position, err := scanPosition(rows)
		if err != nil {
			return nil, fmt.Errorf("scan position: %w", err)
		}
		positions = append(positions, position)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read positions: %w", err)
	}
	return positions, nil
}

func scanPosition(row pgx.Row) (trip.Position, error) {
	var position trip.Position
	err := row.Scan(&position.ID, &position.TripID, &position.Coordinates.Latitude, &position.Coordinates.Longitude,
		&position.RecordedAt, &position.CreatedAt)
	return position, err
}
