package create

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/model"
)

type Usecase struct {
	repo     tripRepository
	tx       txManager
	newID    func() uuid.UUID
	now      func() time.Time
	observer tripObserver
}

func New(repo tripRepository, tx txManager, newID func() uuid.UUID, now func() time.Time, observer tripObserver) *Usecase {
	return &Usecase{repo: repo, tx: tx, newID: newID, now: now, observer: observer}
}

func (u *Usecase) Execute(ctx context.Context, input model.CreateTripInput) (trip.Trip, error) {
	proposed := trip.Trip{
		ID:         u.newID(),
		UserID:     input.UserID,
		DriverID:   input.DriverID,
		StartPoint: input.StartPoint,
		EndPoint:   input.EndPoint,
		Price:      input.Price,
		Status:     trip.StatusActive,
		StartedAt:  u.now().UTC(),
	}
	if err := proposed.Validate(); err != nil {
		return trip.Trip{}, fmt.Errorf("validate trip: %w", err)
	}
	var created trip.Trip
	err := u.tx.Do(ctx, func(ctx context.Context) error {
		var err error
		created, err = u.repo.Insert(ctx, proposed)
		if err != nil {
			return fmt.Errorf("insert trip: %w", err)
		}
		if err := u.repo.AddHistory(ctx, model.TripHistory{TripID: created.ID, ToStatus: trip.StatusActive}); err != nil {
			return fmt.Errorf("record trip creation: %w", err)
		}
		return nil
	})
	if err != nil {
		return trip.Trip{}, fmt.Errorf("create trip: %w", err)
	}
	u.observer.Created(ctx, created.ID)
	return created, nil
}
