package http

import (
	"io"
	"net/http"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"

	"github.com/VladimirovArtem/template/api"
	"github.com/VladimirovArtem/template/internal/http/response"
)

const (
	maxBodyBytes = 1 << 20
	uuidPattern  = `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`
)

func requestValidator(writer *response.Writer) (api.MiddlewareFunc, error) {
	spec, err := api.GetSpec()
	if err != nil {
		return nil, err
	}
	spec.Servers = nil
	spec.Components.Schemas["TripData"].Value.Properties["user_id"].Value.Pattern = uuidPattern
	spec.Components.Schemas["TripData"].Value.Properties["driver_id"].Value.Pattern = uuidPattern
	spec.Components.Parameters["TripId"].Value.Schema.Value.Pattern = uuidPattern
	spec.Components.Parameters["IdempotencyKey"].Value.Schema.Value.Pattern = uuidPattern
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
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
			route, params, err := router.FindRoute(r)
			if err == nil && route.Operation.RequestBody == nil {
				body, readErr := io.ReadAll(io.LimitReader(r.Body, 1))
				if readErr != nil || len(body) != 0 {
					writer.InvalidRequest(w, r)
					return
				}
			}
			if err == nil {
				err = openapi3filter.ValidateRequest(r.Context(), &openapi3filter.RequestValidationInput{
					Request: r, PathParams: params, Route: route, Options: options,
				})
			}
			if err != nil {
				writer.InvalidRequest(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}
