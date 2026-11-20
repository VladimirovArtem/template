package trip

import (
	"time"

	"github.com/google/uuid"
)

type Position struct {
	ID          int64
	TripID      uuid.UUID
	Coordinates Coordinates
	RecordedAt  time.Time
	CreatedAt   time.Time
}

func (p Position) Validate(now time.Time) error {
	if p.TripID == uuid.Nil {
		return &ValidationError{Field: "trip_id", Reason: "must be a non-zero UUID"}
	}
	if err := p.Coordinates.validate("coordinates"); err != nil {
		return err
	}
	if p.RecordedAt.IsZero() {
		return &ValidationError{Field: "recorded_at", Reason: "must be set"}
	}
	if p.RecordedAt.After(now.Add(time.Minute)) {
		return &ValidationError{Field: "recorded_at", Reason: "must not be more than one minute in the future"}
	}
	return nil
}
