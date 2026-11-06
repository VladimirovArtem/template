package trip

import (
	"context"
	"fmt"

	sq "github.com/Masterminds/squirrel"

	"github.com/VladimirovArtem/template/internal/model"
)

func (r *Repository) AddHistory(ctx context.Context, entry model.TripHistory) error {
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
