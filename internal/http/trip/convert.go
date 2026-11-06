package trip

import (
	"github.com/VladimirovArtem/template/api"
	"github.com/VladimirovArtem/template/internal/domain/trip"
)

func ToAPI(tripData trip.Trip) api.Trip {
	return api.Trip{
		Id:         tripData.ID,
		UserId:     tripData.UserID,
		DriverId:   tripData.DriverID,
		Price:      tripData.Price,
		StartPoint: api.Coordinates{Latitude: tripData.StartPoint.Latitude, Longitude: tripData.StartPoint.Longitude},
		EndPoint:   api.Coordinates{Latitude: tripData.EndPoint.Latitude, Longitude: tripData.EndPoint.Longitude},
		Status:     api.TripStatus(tripData.Status),
		StartedAt:  tripData.StartedAt,
		FinishedAt: tripData.FinishedAt,
	}
}
