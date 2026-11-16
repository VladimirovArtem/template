package trip_test

import (
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/google/uuid"

	"github.com/VladimirovArtem/template/internal/domain/trip"
)

const nonZeroUUIDReason = "must be a non-zero UUID"

func TestTripValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*trip.Trip)
		field  string
		reason string
	}{
		{name: "zero price and coordinates"},
		{name: "maximum price", change: func(v *trip.Trip) { v.Price = math.MaxInt64 }},
		{name: "nil trip ID", change: func(v *trip.Trip) { v.ID = uuid.Nil }, field: "id", reason: nonZeroUUIDReason},
		{name: "nil user ID", change: func(v *trip.Trip) { v.UserID = uuid.Nil }, field: "user_id", reason: nonZeroUUIDReason},
		{name: "nil driver ID", change: func(v *trip.Trip) { v.DriverID = uuid.Nil }, field: "driver_id", reason: nonZeroUUIDReason},
		{name: "negative price", change: func(v *trip.Trip) { v.Price = -1 }, field: "price", reason: "must be non-negative"},
	}
	for _, coordinate := range []struct {
		field  string
		limit  float64
		assign func(*trip.Trip, float64)
	}{
		{"start_point.latitude", 90, func(v *trip.Trip, n float64) { v.StartPoint.Latitude = n }},
		{"start_point.longitude", 180, func(v *trip.Trip, n float64) { v.StartPoint.Longitude = n }},
		{"end_point.latitude", 90, func(v *trip.Trip, n float64) { v.EndPoint.Latitude = n }},
		{"end_point.longitude", 180, func(v *trip.Trip, n float64) { v.EndPoint.Longitude = n }},
	} {
		for _, value := range []struct {
			name    string
			number  float64
			invalid bool
		}{
			{"minimum", -coordinate.limit, false},
			{"maximum", coordinate.limit, false},
			{"below minimum", math.Nextafter(-coordinate.limit, math.Inf(-1)), true},
			{"above maximum", math.Nextafter(coordinate.limit, math.Inf(1)), true},
			{"NaN", math.NaN(), true},
			{"positive infinity", math.Inf(1), true},
			{"negative infinity", math.Inf(-1), true},
		} {
			field, reason := "", ""
			if value.invalid {
				field = coordinate.field
				reason = fmt.Sprintf("must be between -%g and %g", coordinate.limit, coordinate.limit)
			}
			tests = append(tests, struct {
				name   string
				change func(*trip.Trip)
				field  string
				reason string
			}{
				name:   coordinate.field + "/" + value.name,
				change: func(v *trip.Trip) { coordinate.assign(v, value.number) },
				field:  field, reason: reason,
			})
		}
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			candidate := trip.Trip{
				ID:       uuid.MustParse("11111111-1111-4111-8111-111111111111"),
				UserID:   uuid.MustParse("22222222-2222-4222-8222-222222222222"),
				DriverID: uuid.MustParse("33333333-3333-4333-8333-333333333333"),
				Status:   trip.StatusActive,
			}
			if tt.change != nil {
				tt.change(&candidate)
			}
			err := candidate.Validate()
			if tt.field == "" {
				if err != nil {
					t.Fatalf("Validate() = %v", err)
				}
				return
			}
			var validationErr *trip.ValidationError
			if !errors.As(err, &validationErr) || !errors.Is(err, trip.ErrInvalidInput) {
				t.Fatalf("Validate() = %v, want ValidationError wrapping ErrInvalidInput", err)
			}
			if validationErr.Field != tt.field || validationErr.Reason != tt.reason || err.Error() != tt.field+": "+tt.reason {
				t.Fatalf("validation details = %#v, want %s: %s", validationErr, tt.field, tt.reason)
			}
		})
	}
}
