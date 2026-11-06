package create

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/VladimirovArtem/template/api"
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

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body api.CreateTripJSONRequestBody
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
	created, err := h.usecase.Execute(r.Context(), toInput(body))
	if err != nil {
		h.writer.Error(w, r, "create trip", err)
		return
	}
	w.Header().Set("Location", "/api/v1/trips/"+created.ID.String())
	h.writer.JSON(w, r, http.StatusCreated, trip.ToAPI(created))
}
