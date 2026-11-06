//go:generate go tool mockgen -source contract.go -destination mocks_test.go -package get_test

package get

import (
	"context"

	"github.com/google/uuid"

	"github.com/VladimirovArtem/template/internal/domain/trip"
)

type tripRepository interface {
	Get(context.Context, uuid.UUID) (trip.Trip, error)
}
