package create

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/google/uuid"

	"github.com/VladimirovArtem/template/api"
	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/http/position"
	"github.com/VladimirovArtem/template/internal/http/response"
	"github.com/VladimirovArtem/template/internal/model"
)

type Handler struct {
	usecase usecase
	writer  *response.Writer
}

func New(usecase usecase, writer *response.Writer) *Handler {
	return &Handler{usecase: usecase, writer: writer}
}

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	var body api.CreateTripPositionJSONRequestBody
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		h.writer.InvalidRequest(w, r)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		h.writer.InvalidRequest(w, r)
		return
	}
	created, err := h.usecase.Execute(r.Context(), model.CreatePositionInput{
		TripID: id, Coordinates: trip.Coordinates{Latitude: body.Coordinates.Latitude, Longitude: body.Coordinates.Longitude},
		RecordedAt: body.RecordedAt,
	})
	if err != nil {
		h.writer.Error(w, r, "create position", err)
		return
	}
	h.writer.JSON(w, r, http.StatusCreated, position.ToAPI(created))
}
