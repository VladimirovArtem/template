package domainerror

import (
	"errors"
	"net/http"
	"strings"

	"github.com/VladimirovArtem/template/api"
	"github.com/VladimirovArtem/template/internal/domain/trip"
)

func Problem(err error) (int, api.Problem) {
	var validationErr *trip.ValidationError
	switch {
	case errors.As(err, &validationErr), errors.Is(err, trip.ErrInvalidInput):
		return http.StatusBadRequest, New(http.StatusBadRequest, "invalid_request", "Invalid request", "Request validation failed")
	case errors.Is(err, trip.ErrNotFound):
		return http.StatusNotFound, New(http.StatusNotFound, "trip_not_found", "Trip not found", "Trip was not found")
	case errors.Is(err, trip.ErrDriverBusy):
		return http.StatusConflict, New(http.StatusConflict, "driver_busy", "Driver busy", "Driver already has an active trip")
	case errors.Is(err, trip.ErrCompleted):
		return http.StatusConflict, New(http.StatusConflict, "trip_completed", "Trip completed", "Operation is not allowed for a completed trip")
	default:
		return http.StatusInternalServerError, New(http.StatusInternalServerError, "internal_error", "Internal Server Error", "Internal server error")
	}
}

func New(status int32, code, title, detail string) api.Problem {
	return api.Problem{
		Type:   "https://tripgo.example/problems/" + strings.ReplaceAll(code, "_", "-"),
		Title:  title,
		Status: status,
		Code:   code,
		Detail: &detail,
	}
}
