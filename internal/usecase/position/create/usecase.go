package create

import (
	"context"
	"fmt"
	"time"

	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/model"
)

type Usecase struct {
	repo     positionRepository
	tx       txManager
	now      func() time.Time
	canceler positionCanceler
	observer positionObserver
}

func New(repo positionRepository, tx txManager, now func() time.Time, canceler positionCanceler, observer positionObserver) *Usecase {
	return &Usecase{repo: repo, tx: tx, now: now, canceler: canceler, observer: observer}
}

func (u *Usecase) Execute(ctx context.Context, input model.CreatePositionInput) (trip.Position, error) {
	proposed := trip.Position{TripID: input.TripID, Coordinates: input.Coordinates, RecordedAt: input.RecordedAt.UTC()}
	if err := proposed.Validate(u.now()); err != nil {
		return trip.Position{}, fmt.Errorf("validate position: %w", err)
	}
	var created trip.Position
	err := u.tx.Do(ctx, func(ctx context.Context) error {
		if err := u.repo.UpdateLastPositionAt(ctx, proposed.TripID, proposed.RecordedAt); err != nil {
			return fmt.Errorf("update trip for position: %w", err)
		}
		var err error
		created, err = u.repo.InsertPosition(ctx, proposed)
		if err != nil {
			return fmt.Errorf("save position: %w", err)
		}
		return nil
	})
	if err != nil {
		return trip.Position{}, fmt.Errorf("create position: %w", err)
	}
	u.canceler.Cancel(ctx, created.TripID)
	u.observer.Saved(ctx)
	return created, nil
}
