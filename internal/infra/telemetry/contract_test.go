//go:generate go tool mockgen -source contract_test.go -destination mocks_test.go -package telemetry_test

package telemetry_test

import (
	"context"

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

var (
	_ tripRepository = (*MocktripRepository)(nil)
	_ txManager      = (*MocktxManager)(nil)
	_ activeTrips    = (*MockactiveTrips)(nil)
)
