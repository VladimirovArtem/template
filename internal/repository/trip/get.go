package trip

import (
	"context"
	"errors"
	"fmt"
	"strings"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/VladimirovArtem/template/internal/domain/trip"
)

func (r *Repository) Get(ctx context.Context, id uuid.UUID) (trip.Trip, error) {
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
