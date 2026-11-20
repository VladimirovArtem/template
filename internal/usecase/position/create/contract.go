//go:generate go tool mockgen -source contract.go -destination mocks_test.go -package create_test

package create

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/VladimirovArtem/template/internal/domain/trip"
)

type positionRepository interface {
	UpdateLastPositionAt(context.Context, uuid.UUID, time.Time) error
	InsertPosition(context.Context, trip.Position) (trip.Position, error)
}

type txManager interface {
	Do(context.Context, func(context.Context) error) error
}

type positionCanceler interface {
	Cancel(context.Context, uuid.UUID) bool
}

type positionObserver interface {
	Saved(context.Context)
}
