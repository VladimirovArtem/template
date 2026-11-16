package trip_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/VladimirovArtem/template/api"
	domain "github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/http/trip"
)

func TestToAPI(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.November, 1, 9, 0, 0, 0, time.UTC)
	for _, completed := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "completed"}[completed], func(t *testing.T) {
			t.Parallel()
			value := domain.Trip{
				ID:         uuid.MustParse("11111111-1111-4111-8111-111111111111"),
				UserID:     uuid.MustParse("22222222-2222-4222-8222-222222222222"),
				DriverID:   uuid.MustParse("33333333-3333-4333-8333-333333333333"),
				StartPoint: domain.Coordinates{Latitude: -90, Longitude: -180},
				EndPoint:   domain.Coordinates{Latitude: 90, Longitude: 180},
				Price:      9007199254740993, Status: domain.StatusActive, StartedAt: at,
				CreatedAt: at, UpdatedAt: at,
			}
			want := api.Trip{
				Id: value.ID, UserId: value.UserID, DriverId: value.DriverID,
				StartPoint: api.Coordinates{Latitude: -90, Longitude: -180},
				EndPoint:   api.Coordinates{Latitude: 90, Longitude: 180},
				Price:      9007199254740993, Status: api.Active, StartedAt: at,
			}
			if completed {
				finishedAt := at.Add(time.Hour)
				value.Status, value.FinishedAt = domain.StatusCompleted, &finishedAt
				want.Status, want.FinishedAt = api.Completed, &finishedAt
			}
			got := trip.ToAPI(value)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("ToAPI() = %+v, want %+v", got, want)
			}
			data, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			for _, absent := range []string{"created_at", "updated_at", "last_position_at"} {
				if _, exists := fields[absent]; exists {
					t.Fatalf("unexpected response field %s", absent)
				}
			}
			_, hasFinish := fields["finished_at"]
			if hasFinish != completed {
				t.Fatalf("finished_at present = %t", hasFinish)
			}
		})
	}
}
