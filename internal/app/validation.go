package app

import (
	"io"
	"net/http"

	"github.com/VladimirovArtem/template/api"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
)

func init() {

	openapi3.DefineStringFormatValidator("uuid", openapi3.NewRegexpFormatValidator(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`))
}

func (h *handlers) requestValidator() (api.MiddlewareFunc, error) {
	spec, err := api.GetSpec()
	if err != nil {
		return nil, err
	}

	spec.Servers = nil
	router, err := legacy.NewRouter(spec)
	if err != nil {
		return nil, err
	}
	options := &openapi3filter.Options{SkipSettingDefaults: true}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

			if r.URL.Path == "/health" || r.URL.Path == "/ready" {
				next.ServeHTTP(w, r)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			route, params, err := router.FindRoute(r)
			if err == nil && route.Operation.RequestBody == nil {

				body, readErr := io.ReadAll(io.LimitReader(r.Body, 1))
				if readErr != nil || len(body) != 0 {
					h.requestError(w, r)
					return
				}
			}
			if err == nil {
				err = openapi3filter.ValidateRequest(r.Context(), &openapi3filter.RequestValidationInput{
					Request:    r,
					PathParams: params,
					Route:      route,
					Options:    options,
				})
			}
			if err != nil {
				h.requestError(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}
