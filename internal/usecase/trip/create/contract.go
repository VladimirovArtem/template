//go:generate go tool mockgen -source contract.go -destination mocks_test.go -package create_test

package create

import (
	"context"

	"github.com/google/uuid"

	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/model"
)

type tripRepository interface {
	Insert(context.Context, trip.Trip) (trip.Trip, error)
	AddHistory(context.Context, model.TripHistory) error
}

type txManager interface {
	Do(context.Context, func(context.Context) error) error
}

type tripObserver interface {
	Created(context.Context, uuid.UUID)
}
