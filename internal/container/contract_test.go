//go:generate go tool mockgen -source contract_test.go -destination test_mocks_test.go -package container

package container

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/model"
)

type scanRepository interface {
	StillStale(context.Context, model.StaleTrip) (bool, error)
	ListStaleTrips(context.Context, time.Time, time.Duration, int, *model.StaleTripCursor) ([]model.StaleTrip, error)
}

type positionGateway interface {
	RequestPosition(context.Context, model.PositionRequest) error
}

type positionRepository interface {
	UpdateLastPositionAt(context.Context, uuid.UUID, time.Time) error
	InsertPosition(context.Context, trip.Position) (trip.Position, error)
}

type txManager interface {
	Do(context.Context, func(context.Context) error) error
}

type scanTicker interface {
	Ticks() <-chan time.Time
	Stop()
}

var (
	_ scanRepository     = (*MockscanRepository)(nil)
	_ positionGateway    = (*MockpositionGateway)(nil)
	_ positionRepository = (*MockpositionRepository)(nil)
	_ txManager          = (*MocktxManager)(nil)
	_ scanTicker         = (*MockscanTicker)(nil)
)
