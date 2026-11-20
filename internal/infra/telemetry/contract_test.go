//go:generate go tool mockgen -source contract_test.go -destination mocks_test.go -package telemetry_test

package telemetry_test

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/model"
)

type tripRepository interface {
	Insert(context.Context, trip.Trip) (trip.Trip, error)
	Get(context.Context, uuid.UUID) (trip.Trip, error)
	Finish(context.Context, trip.Trip) (trip.Trip, error)
	AddHistory(context.Context, model.TripHistory) error
}

type txManager interface {
	Do(context.Context, func(context.Context) error) error
}

type activeTrips interface {
	CountActive(context.Context) (int64, error)
}

type positionRepository interface {
	StillStale(context.Context, model.StaleTrip) (bool, error)
	UpdateLastPositionAt(context.Context, uuid.UUID, time.Time) error
	InsertPosition(context.Context, trip.Position) (trip.Position, error)
}

type positionGateway interface {
	RequestPosition(context.Context, model.PositionRequest) error
}

var (
	_ tripRepository     = (*MocktripRepository)(nil)
	_ txManager          = (*MocktxManager)(nil)
	_ activeTrips        = (*MockactiveTrips)(nil)
	_ positionRepository = (*MockpositionRepository)(nil)
	_ positionGateway    = (*MockpositionGateway)(nil)
)
