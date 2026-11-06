package model

import (
	"github.com/google/uuid"

	"github.com/VladimirovArtem/template/internal/domain/trip"
)

type CreateTripInput struct {
	UserID     uuid.UUID
	DriverID   uuid.UUID
	StartPoint trip.Coordinates
	EndPoint   trip.Coordinates
	Price      int64
}

type TripHistory struct {
	TripID     uuid.UUID
	FromStatus *trip.Status
	ToStatus   trip.Status
	Reason     *string
}
