package model

import "github.com/google/uuid"

type PositionRequest struct {
	RequestID uuid.UUID
	TripID    uuid.UUID
	DriverID  uuid.UUID
}
