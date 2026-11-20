package list

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/VladimirovArtem/template/internal/domain/trip"
)

type Usecase struct {
	repo tripRepository
}

func New(repo tripRepository) *Usecase {
	return &Usecase{repo: repo}
}

func (u *Usecase) Execute(ctx context.Context, id uuid.UUID) ([]trip.Position, error) {
	if id == uuid.Nil {
		return nil, &trip.ValidationError{Field: "trip_id", Reason: "must be a non-zero UUID"}
	}
	if _, err := u.repo.Get(ctx, id); err != nil {
		return nil, fmt.Errorf("get trip for route: %w", err)
	}
	positions, err := u.repo.ListPositions(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list positions: %w", err)
	}
	return positions, nil
}
