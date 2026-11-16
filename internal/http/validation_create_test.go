package http_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/VladimirovArtem/template/api"
	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/model"
)

func TestValidCreateRequests(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name       string
		price      int64
		start, end trip.Coordinates
		padToLimit bool
	}{
		{name: "zero values"},
		{name: "above float64 exact range", price: 9007199254740993},
		{name: "maximum int64", price: math.MaxInt64},
		{name: "coordinate boundaries", start: trip.Coordinates{Latitude: -90, Longitude: -180}, end: trip.Coordinates{Latitude: 90, Longitude: 180}},
		{name: "exact body limit", padToLimit: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			router := newTestRouter(t)
			input := model.CreateTripInput{
				UserID:     uuid.MustParse("22222222-2222-4222-8222-222222222222"),
				DriverID:   uuid.MustParse("33333333-3333-4333-8333-333333333333"),
				StartPoint: tt.start, EndPoint: tt.end, Price: tt.price,
			}
			body := fmt.Sprintf(`{"user_id":%q,"driver_id":%q,"start_point":{"latitude":%g,"longitude":%g},"end_point":{"latitude":%g,"longitude":%g},"price":%d}`,
				input.UserID.String(), input.DriverID.String(), tt.start.Latitude, tt.start.Longitude, tt.end.Latitude, tt.end.Longitude, tt.price)
			if tt.padToLimit {
				body += strings.Repeat(" ", (1<<20)-len(body))
			} else {
				body += " \n\t"
			}
			id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
			ctx := context.WithValue(t.Context(), requestKey{}, t.Name())
			router.create.EXPECT().Execute(gomock.Any(), input).DoAndReturn(func(ctx context.Context, _ model.CreateTripInput) (trip.Trip, error) {
				if ctx.Value(requestKey{}) != t.Name() || ctx.Done() != t.Context().Done() {
					t.Fatal("request context values or cancellation were lost")
				}
				return trip.Trip{ID: id, Price: tt.price, Status: trip.StatusActive}, nil
			})
			request := httptest.NewRequest("POST", "/api/v1/trips", strings.NewReader(body)).WithContext(ctx)
			request.Header.Set("Content-Type", jsonContentType)
			request.Header.Set("Idempotency-Key", "44444444-4444-4444-8444-444444444444")
			recorder := httptest.NewRecorder()
			router.handler.ServeHTTP(recorder, request)
			var result api.Trip
			if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != 201 || recorder.Header().Get("Content-Type") != jsonContentType ||
				recorder.Header().Get("Location") != "/api/v1/trips/"+id.String() || result.Id != id || result.Price != tt.price {
				t.Fatalf("create response: %d %s", recorder.Code, recorder.Body)
			}
		})
	}
}
