//go:generate go tool mockgen -source contract_test.go -destination mocks_test.go -package http_test

package http_test

import (
	"context"

	"github.com/google/uuid"

	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/model"
)

type creator interface {
	Execute(context.Context, model.CreateTripInput) (trip.Trip, error)
}

type tripUsecase interface {
	Execute(context.Context, uuid.UUID) (trip.Trip, error)
}

type pinger interface {
	Ping(context.Context) error
}

var (
	_ creator     = (*Mockcreator)(nil)
	_ tripUsecase = (*MocktripUsecase)(nil)
	_ pinger      = (*Mockpinger)(nil)
)
