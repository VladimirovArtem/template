package trip

import (
	"math"

	"github.com/google/uuid"
)

func (t Trip) Validate() error {
	for _, field := range []struct {
		name  string
		value uuid.UUID
	}{
		{"id", t.ID},
		{"user_id", t.UserID},
		{"driver_id", t.DriverID},
	} {
		if field.value == uuid.Nil {
			return &ValidationError{Field: field.name, Reason: "must be a non-zero UUID"}
		}
	}
	if t.Price < 0 {
		return &ValidationError{Field: "price", Reason: "must be non-negative"}
	}
	for _, point := range []struct {
		name  string
		value Coordinates
	}{
		{"start_point", t.StartPoint},
		{"end_point", t.EndPoint},
	} {
		if err := point.value.validate(point.name); err != nil {
			return err
		}
	}
	return nil
}

func (p Coordinates) validate(field string) error {
	if math.IsNaN(p.Latitude) || p.Latitude < -90 || p.Latitude > 90 {
		return &ValidationError{Field: field + ".latitude", Reason: "must be between -90 and 90"}
	}
	if math.IsNaN(p.Longitude) || p.Longitude < -180 || p.Longitude > 180 {
		return &ValidationError{Field: field + ".longitude", Reason: "must be between -180 and 180"}
	}
	return nil
}
