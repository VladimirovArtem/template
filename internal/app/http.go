package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/VladimirovArtem/template/api"
	"github.com/VladimirovArtem/template/internal/trip"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type tripOperations interface {
	Create(context.Context, trip.CreateInput) (trip.Trip, error)
	Get(context.Context, uuid.UUID) (trip.Trip, error)
	Finish(context.Context, uuid.UUID) (trip.Trip, error)
}

type handlers struct {
	trips        tripOperations
	pool         *pgxpool.Pool
	queryTimeout time.Duration
	logger       *slog.Logger
}

var _ api.ServerInterface = (*handlers)(nil)

func newRouter(pool *pgxpool.Pool, queryTimeout time.Duration, logger *slog.Logger, trips tripOperations) (http.Handler, error) {
	h := &handlers{pool: pool, queryTimeout: queryTimeout, logger: logger, trips: trips}
	validate, err := h.requestValidator()
	if err != nil {
		return nil, err
	}
	router := chi.NewRouter()
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		h.problem(w, r, http.StatusNotFound, "not_found", "Not Found", "Route was not found")
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		h.problem(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "Method Not Allowed", "Method is not allowed for this route")
	})
	return api.HandlerWithOptions(h, api.ChiServerOptions{
		BaseRouter:  router,
		Middlewares: []api.MiddlewareFunc{validate},
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, _ error) {
			h.requestError(w, r)
		},
	}), nil
}

func (h *handlers) Health(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(r.Context(), w, http.StatusOK, "application/json", api.HealthResponse{Status: api.Ok})
}

func (h *handlers) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.queryTimeout)
	defer cancel()
	status, body := http.StatusOK, api.HealthResponse{Status: api.Ok}
	if err := h.pool.Ping(ctx); err != nil {
		h.logger.WarnContext(ctx, "PostgreSQL readiness check failed", "error", databaseError{cause: err})
		status, body.Status = http.StatusServiceUnavailable, api.Unavailable
	}
	h.writeJSON(r.Context(), w, status, "application/json", body)
}

func (h *handlers) CreateTrip(w http.ResponseWriter, r *http.Request, _ api.CreateTripParams) {

	var body api.CreateTripJSONRequestBody
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		h.requestError(w, r)
		return
	}

	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		h.requestError(w, r)
		return
	}
	if body.UserId == uuid.Nil || body.DriverId == uuid.Nil {
		h.requestError(w, r)
		return
	}
	createdTrip, err := h.trips.Create(r.Context(), trip.CreateInput{
		UserID:     body.UserId,
		DriverID:   body.DriverId,
		Price:      body.Price,
		StartPoint: trip.Coordinates{Latitude: body.StartPoint.Latitude, Longitude: body.StartPoint.Longitude},
		EndPoint:   trip.Coordinates{Latitude: body.EndPoint.Latitude, Longitude: body.EndPoint.Longitude},
	})
	if err != nil {
		h.operationError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/trips/"+createdTrip.ID.String())
	h.writeJSON(r.Context(), w, http.StatusCreated, "application/json", toAPITrip(createdTrip))
}

func (h *handlers) GetTrip(w http.ResponseWriter, r *http.Request, id api.TripId) {
	foundTrip, err := h.trips.Get(r.Context(), id)
	if err != nil {
		h.operationError(w, r, err)
		return
	}
	h.writeJSON(r.Context(), w, http.StatusOK, "application/json", toAPITrip(foundTrip))
}

func (h *handlers) FinishTrip(w http.ResponseWriter, r *http.Request, id api.TripId) {
	finishedTrip, err := h.trips.Finish(r.Context(), id)
	if err != nil {
		h.operationError(w, r, err)
		return
	}
	h.writeJSON(r.Context(), w, http.StatusOK, "application/json", toAPITrip(finishedTrip))
}

func toAPITrip(tripData trip.Trip) api.Trip {
	return api.Trip{
		Id:             tripData.ID,
		UserId:         tripData.UserID,
		DriverId:       tripData.DriverID,
		Price:          tripData.Price,
		StartPoint:     api.Coordinates{Latitude: tripData.StartPoint.Latitude, Longitude: tripData.StartPoint.Longitude},
		EndPoint:       api.Coordinates{Latitude: tripData.EndPoint.Latitude, Longitude: tripData.EndPoint.Longitude},
		Status:         api.TripStatus(tripData.Status),
		StartedAt:      tripData.StartedAt,
		FinishedAt:     tripData.FinishedAt,
		LastPositionAt: nil,
	}
}

func (h *handlers) requestError(w http.ResponseWriter, r *http.Request) {
	h.problem(w, r, http.StatusBadRequest, "invalid_request", "Invalid request", "Request validation failed")
}

func (h *handlers) operationError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, trip.ErrInvalidInput):
		h.requestError(w, r)
	case errors.Is(err, trip.ErrNotFound):
		h.problem(w, r, http.StatusNotFound, "trip_not_found", "Trip not found", "Trip was not found")
	case errors.Is(err, trip.ErrDriverBusy):
		h.problem(w, r, http.StatusConflict, "driver_busy", "Driver busy", "Driver already has an active trip")
	case errors.Is(err, trip.ErrCompleted):
		h.problem(w, r, http.StatusConflict, "trip_completed", "Trip completed", "Operation is not allowed for a completed trip")
	default:
		h.logger.ErrorContext(r.Context(), "trip operation failed", "error", databaseError{cause: err})
		h.problem(w, r, http.StatusInternalServerError, "internal_error", "Internal Server Error", "Internal server error")
	}
}

func (h *handlers) problem(w http.ResponseWriter, r *http.Request, status int, code, title, detail string) {
	instance := r.URL.Path
	h.writeJSON(r.Context(), w, status, "application/problem+json", api.Problem{
		Type:     "https://tripgo.example/problems/" + strings.ReplaceAll(code, "_", "-"),
		Title:    title,
		Status:   int32(status),
		Code:     code,
		Detail:   &detail,
		Instance: &instance,
	})
}

func (h *handlers) writeJSON(ctx context.Context, w http.ResponseWriter, status int, contentType string, body any) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.WarnContext(ctx, "HTTP response write failed")
	}
}
