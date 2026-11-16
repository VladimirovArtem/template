//go:generate go tool mockgen -source contract.go -destination mocks_test.go -package create_test

package create

import (
	"context"

	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/model"
)

type usecase interface {
	Execute(context.Context, model.CreateTripInput) (trip.Trip, error)
}
