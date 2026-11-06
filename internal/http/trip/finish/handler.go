package finish

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/VladimirovArtem/template/internal/http/response"
	"github.com/VladimirovArtem/template/internal/http/trip"
)

type Handler struct {
	usecase usecase
	writer  *response.Writer
}

func New(usecase usecase, writer *response.Writer) *Handler {
	return &Handler{usecase: usecase, writer: writer}
}

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	finished, err := h.usecase.Execute(r.Context(), id)
	if err != nil {
		h.writer.Error(w, r, "finish trip", err)
		return
	}
	h.writer.JSON(w, r, http.StatusOK, trip.ToAPI(finished))
}
