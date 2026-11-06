package trip

import (
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusActive    Status = "active"
	StatusCompleted Status = "completed"
)

type Coordinates struct {
	Latitude  float64
	Longitude float64
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

func (t *Trip) Finish(at time.Time) error {
	if t.Status != StatusActive {
		return ErrCompleted
	}
	t.Status = StatusCompleted
	t.FinishedAt = &at
	t.UpdatedAt = at
	return nil
}
