//go:generate go tool mockgen -source contract.go -destination mocks_test.go -package list_test

package list

import (
	"context"

	"github.com/google/uuid"

	"github.com/VladimirovArtem/template/internal/domain/trip"
)

type tripRepository interface {
	Get(context.Context, uuid.UUID) (trip.Trip, error)
	ListPositions(context.Context, uuid.UUID) ([]trip.Position, error)
}
