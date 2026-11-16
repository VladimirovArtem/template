package domainerror_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/http/domainerror"
)

func TestProblem(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, code, title, detail, suffix string
		err                               error
		status                            int
	}{
		{"invalid input", "invalid_request", "Invalid request", "Request validation failed", "invalid-request", trip.ErrInvalidInput, 400},
		{"validation details", "invalid_request", "Invalid request", "Request validation failed", "invalid-request", &trip.ValidationError{Field: "price", Reason: "negative"}, 400},
		{"missing trip", "trip_not_found", "Trip not found", "Trip was not found", "trip-not-found", trip.ErrNotFound, 404},
		{"busy driver", "driver_busy", "Driver busy", "Driver already has an active trip", "driver-busy", trip.ErrDriverBusy, 409},
		{"completed trip", "trip_completed", "Trip completed", "Operation is not allowed for a completed trip", "trip-completed", trip.ErrCompleted, 409},
		{"unknown error", "internal_error", "Internal Server Error", "Internal server error", "internal-error", errors.New("SELECT secret FROM database"), 500},
		{"nil error", "internal_error", "Internal Server Error", "Internal server error", "internal-error", nil, 500},
	} {
		for _, wrapped := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/wrapped=%t", tt.name, wrapped), func(t *testing.T) {
				t.Parallel()
				err := tt.err
				if wrapped && err != nil {
					err = fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", err))
				}
				status, body := domainerror.Problem(err)
				if status != tt.status || int(body.Status) != tt.status || body.Code != tt.code ||
					body.Title != tt.title || body.Type != "https://tripgo.example/problems/"+tt.suffix ||
					body.Detail == nil || *body.Detail != tt.detail || body.Instance != nil {
					t.Fatalf("Problem() = %d, %+v", status, body)
				}
			})
		}
	}
}
