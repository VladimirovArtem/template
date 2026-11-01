package trip

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
)

type TxManager interface {
	Do(ctx context.Context, fn func(context.Context) error) error
}

type Repository interface {
	Insert(ctx context.Context, tripData Trip) (Trip, error)
	Get(ctx context.Context, id uuid.UUID) (Trip, error)
	Finish(ctx context.Context, id uuid.UUID, finishedAt time.Time) (Trip, error)
	AddHistory(ctx context.Context, entry History) error
}

type Service struct {
	repo Repository
	tx   TxManager
}

func NewService(repo Repository, tx TxManager) *Service {
	return &Service{repo: repo, tx: tx}
}

func (s *Service) Create(ctx context.Context, input CreateInput) (Trip, error) {
	if input.UserID == uuid.Nil || input.DriverID == uuid.Nil || input.Price < 0 ||
		!validCoordinates(input.StartPoint) || !validCoordinates(input.EndPoint) {
		return Trip{}, ErrInvalidInput
	}
	var created Trip
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		var err error
		created, err = s.repo.Insert(ctx, Trip{
			ID:         uuid.New(),
			UserID:     input.UserID,
			DriverID:   input.DriverID,
			StartPoint: input.StartPoint,
			EndPoint:   input.EndPoint,
			Price:      input.Price,
			Status:     Active,
			StartedAt:  time.Now().UTC(),
		})
		if err != nil {
			return fmt.Errorf("insert trip: %w", err)
		}
		if err := s.repo.AddHistory(ctx, History{TripID: created.ID, ToStatus: Active}); err != nil {
			return fmt.Errorf("record trip creation: %w", err)
		}
		return nil
	})
	if err != nil {
		return Trip{}, fmt.Errorf("create trip: %w", err)
	}
	return created, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (Trip, error) {
	foundTrip, err := s.repo.Get(ctx, id)
	if err != nil {
		return Trip{}, fmt.Errorf("get trip: %w", err)
	}
	return foundTrip, nil
}

func (s *Service) Finish(ctx context.Context, id uuid.UUID) (Trip, error) {
	var finished Trip
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		var err error
		finished, err = s.repo.Finish(ctx, id, time.Now().UTC())
		if err != nil {
			return fmt.Errorf("finish trip: %w", err)
		}
		from := Active
		if err := s.repo.AddHistory(ctx, History{TripID: id, FromStatus: &from, ToStatus: Completed}); err != nil {
			return fmt.Errorf("record trip completion: %w", err)
		}
		return nil
	})
	if err != nil {
		return Trip{}, fmt.Errorf("finish trip: %w", err)
	}
	return finished, nil
}

func validCoordinates(p Coordinates) bool {
	return !math.IsNaN(p.Latitude) && !math.IsNaN(p.Longitude) &&
		p.Latitude >= -90 && p.Latitude <= 90 && p.Longitude >= -180 && p.Longitude <= 180
}
