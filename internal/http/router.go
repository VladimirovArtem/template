package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/VladimirovArtem/template/api"
	"github.com/VladimirovArtem/template/internal/http/domainerror"
	"github.com/VladimirovArtem/template/internal/http/health"
	"github.com/VladimirovArtem/template/internal/http/middleware"
	"github.com/VladimirovArtem/template/internal/http/response"
	"github.com/VladimirovArtem/template/internal/http/trip/create"
	"github.com/VladimirovArtem/template/internal/http/trip/finish"
	"github.com/VladimirovArtem/template/internal/http/trip/get"
)

func NewRouter(create *create.Handler, get *get.Handler, finish *finish.Handler, health *health.Handler, writer *response.Writer, instrumentation *middleware.HTTP) (http.Handler, error) {
	validate, err := requestValidator(writer)
	if err != nil {
		return nil, err
	}
	router := chi.NewRouter()
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writer.Problem(w, r, http.StatusNotFound, domainerror.New(http.StatusNotFound, "not_found", "Not Found", "Route was not found"))
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writer.Problem(w, r, http.StatusMethodNotAllowed, domainerror.New(http.StatusMethodNotAllowed, "method_not_allowed", "Method Not Allowed", "Method is not allowed for this route"))
	})
	handler := api.HandlerWithOptions(&adapter{create: create, get: get, finish: finish, health: health}, api.ChiServerOptions{
		BaseRouter: router, Middlewares: []api.MiddlewareFunc{validate},
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, _ error) { writer.InvalidRequest(w, r) },
	})
	return instrumentation.Wrap(handler, router), nil
}
