package position

import (
	"github.com/VladimirovArtem/template/api"
	"github.com/VladimirovArtem/template/internal/domain/trip"
)

func ToAPI(position trip.Position) api.TripPosition {
	return api.TripPosition{
		Id: position.ID, TripId: position.TripID,
		Coordinates: api.Coordinates{Latitude: position.Coordinates.Latitude, Longitude: position.Coordinates.Longitude},
		RecordedAt:  position.RecordedAt,
	}
}
