package finish

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
	now      func() time.Time
	observer tripObserver
}

func New(repo tripRepository, tx txManager, now func() time.Time, observer tripObserver) *Usecase {
	return &Usecase{repo: repo, tx: tx, now: now, observer: observer}
}

func (u *Usecase) Execute(ctx context.Context, id uuid.UUID) (trip.Trip, error) {
	var finished trip.Trip
	err := u.tx.Do(ctx, func(ctx context.Context) error {
		current, err := u.repo.Get(ctx, id)
		if err != nil {
			return fmt.Errorf("get trip for completion: %w", err)
		}
		from := current.Status
		if err := current.Finish(u.now().UTC()); err != nil {
			return fmt.Errorf("complete trip: %w", err)
		}
		finished, err = u.repo.Finish(ctx, current)
		if err != nil {
			return fmt.Errorf("save trip completion: %w", err)
		}
		if err := u.repo.AddHistory(ctx, model.TripHistory{TripID: id, FromStatus: &from, ToStatus: trip.StatusCompleted}); err != nil {
			return fmt.Errorf("record trip completion: %w", err)
		}
		return nil
	})
	if err != nil {
		return trip.Trip{}, fmt.Errorf("finish trip: %w", err)
	}
	u.observer.Completed(ctx, finished.ID)
	return finished, nil
}
