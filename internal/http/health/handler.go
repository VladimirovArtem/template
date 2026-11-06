package health

import (
	"context"
	"net/http"
	"time"

	"github.com/VladimirovArtem/template/api"
	"github.com/VladimirovArtem/template/internal/http/response"
)

type Handler struct {
	pinger  pinger
	timeout time.Duration
	writer  *response.Writer
}

func New(pinger pinger, timeout time.Duration, writer *response.Writer) *Handler {
	return &Handler{pinger: pinger, timeout: timeout, writer: writer}
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	h.writer.JSON(w, r, http.StatusOK, api.HealthResponse{Status: api.Ok})
}

func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()
	status, body := http.StatusOK, api.HealthResponse{Status: api.Ok}
	if err := h.pinger.Ping(ctx); err != nil {
		h.writer.RecordError(r, "check PostgreSQL readiness", err)
		status, body.Status = http.StatusServiceUnavailable, api.Unavailable
	}
	h.writer.JSON(w, r, status, body)
}
