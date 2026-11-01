package trip

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound     = errors.New("trip not found")
	ErrDriverBusy   = errors.New("driver busy")
	ErrCompleted    = errors.New("trip already completed")
	ErrInvalidInput = errors.New("invalid trip input")
)

type Status string

const (
	Active    Status = "active"
	Completed Status = "completed"
)

type Coordinates struct {
	Latitude  float64
	Longitude float64
}

type CreateInput struct {
	UserID     uuid.UUID
	DriverID   uuid.UUID
	StartPoint Coordinates
	EndPoint   Coordinates
	Price      int64
}

type Trip struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	DriverID   uuid.UUID
	StartPoint Coordinates
	EndPoint   Coordinates
	Price      int64
	Status     Status
	StartedAt  time.Time
	FinishedAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type History struct {
	TripID     uuid.UUID
	FromStatus *Status
	ToStatus   Status
	Reason     *string
}
