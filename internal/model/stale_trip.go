package model

import (
	"time"

	"github.com/google/uuid"
)

type StaleTrip struct {
	ID          uuid.UUID
	DriverID    uuid.UUID
	ReferenceAt time.Time
}

type StaleTripCursor struct {
	ReferenceAt time.Time
	ID          uuid.UUID
}
