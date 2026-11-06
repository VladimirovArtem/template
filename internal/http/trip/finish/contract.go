//go:generate go tool mockgen -source contract.go -destination mocks_test.go -package finish_test

package finish

import (
	"context"

	"github.com/google/uuid"

	"github.com/VladimirovArtem/template/internal/domain/trip"
)

type usecase interface {
	Execute(context.Context, uuid.UUID) (trip.Trip, error)
}
