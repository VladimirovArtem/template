package list

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/VladimirovArtem/template/api"
	"github.com/VladimirovArtem/template/internal/http/position"
	"github.com/VladimirovArtem/template/internal/http/response"
)

type Handler struct {
	usecase usecase
	writer  *response.Writer
}

func New(usecase usecase, writer *response.Writer) *Handler {
	return &Handler{usecase: usecase, writer: writer}
}

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	positions, err := h.usecase.Execute(r.Context(), id)
	if err != nil {
		h.writer.Error(w, r, "list positions", err)
		return
	}
	body := api.TripPositionsResponse{Positions: make([]api.TripPosition, 0, len(positions))}
	for _, point := range positions {
		body.Positions = append(body.Positions, position.ToAPI(point))
	}
	h.writer.JSON(w, r, http.StatusOK, body)
}
