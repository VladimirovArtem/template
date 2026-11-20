package trip_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/VladimirovArtem/template/internal/domain/trip"
)

func TestPositionValidate(t *testing.T) {
	t.Parallel()
	const (
		latitudeField  = "coordinates.latitude"
		longitudeField = "coordinates.longitude"
	)
	now := time.Date(2026, 11, 1, 9, 0, 0, 123456000, time.UTC)
	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	for _, tt := range []struct {
		name, field string
		change      func(*trip.Position)
	}{
		{name: "zero coordinates"},
		{name: "minimum coordinates", change: func(p *trip.Position) { p.Coordinates = trip.Coordinates{Latitude: -90, Longitude: -180} }},
		{name: "maximum coordinates", change: func(p *trip.Position) { p.Coordinates = trip.Coordinates{Latitude: 90, Longitude: 180} }},
		{name: "old point", change: func(p *trip.Position) { p.RecordedAt = now.Add(-24 * time.Hour) }},
		{name: "future boundary", change: func(p *trip.Position) { p.RecordedAt = now.Add(time.Minute) }},
		{name: "timezone", change: func(p *trip.Position) { p.RecordedAt = now.Add(time.Minute).In(time.FixedZone("MSK", 3*60*60)) }},
		{name: "missing trip id", field: "trip_id", change: func(p *trip.Position) { p.TripID = uuid.Nil }},
		{name: "missing timestamp", field: "recorded_at", change: func(p *trip.Position) { p.RecordedAt = time.Time{} }},
		{name: "future past boundary", field: "recorded_at", change: func(p *trip.Position) { p.RecordedAt = now.Add(time.Minute + time.Nanosecond) }},
		{name: "latitude too small", field: latitudeField, change: func(p *trip.Position) { p.Coordinates.Latitude = -90.01 }},
		{name: "latitude too large", field: latitudeField, change: func(p *trip.Position) { p.Coordinates.Latitude = 90.01 }},
		{name: "longitude too small", field: longitudeField, change: func(p *trip.Position) { p.Coordinates.Longitude = -180.01 }},
		{name: "longitude too large", field: longitudeField, change: func(p *trip.Position) { p.Coordinates.Longitude = 180.01 }},
		{name: "latitude NaN", field: latitudeField, change: func(p *trip.Position) { p.Coordinates.Latitude = math.NaN() }},
		{name: "longitude NaN", field: longitudeField, change: func(p *trip.Position) { p.Coordinates.Longitude = math.NaN() }},
		{name: "latitude infinity", field: latitudeField, change: func(p *trip.Position) { p.Coordinates.Latitude = math.Inf(1) }},
		{name: "longitude infinity", field: longitudeField, change: func(p *trip.Position) { p.Coordinates.Longitude = math.Inf(-1) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			position := trip.Position{TripID: id, RecordedAt: now}
			if tt.change != nil {
				tt.change(&position)
			}
			err := position.Validate(now)
			if tt.field == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var validationErr *trip.ValidationError
			if !errors.Is(err, trip.ErrInvalidInput) || !errors.As(err, &validationErr) || validationErr.Field != tt.field || validationErr.Reason == "" {
				t.Fatalf("Validate() = %v, want validation error for %s", err, tt.field)
			}
		})
	}
}
