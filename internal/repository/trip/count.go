package trip

import (
	"context"
	"fmt"

	"github.com/VladimirovArtem/template/internal/domain/trip"
)

func (r *Repository) CountActive(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	var count int64
	if err := executorFor(ctx, r.pool).QueryRow(ctx, "SELECT count(*) FROM trips WHERE status = $1", trip.StatusActive).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active trips: %w", err)
	}
	return count, nil
}
