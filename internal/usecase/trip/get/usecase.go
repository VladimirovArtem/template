package get

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

func (u *Usecase) Execute(ctx context.Context, id uuid.UUID) (trip.Trip, error) {
	found, err := u.repo.Get(ctx, id)
	if err != nil {
		return trip.Trip{}, fmt.Errorf("get trip: %w", err)
	}
	return found, nil
}
