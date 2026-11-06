//go:generate go tool mockgen -source contract.go -destination mocks_test.go -package get_test

package get

import (
	"context"

	"github.com/google/uuid"

	"github.com/VladimirovArtem/template/internal/domain/trip"
)

type usecase interface {
	Execute(context.Context, uuid.UUID) (trip.Trip, error)
}
