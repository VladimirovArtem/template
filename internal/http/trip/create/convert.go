package create

import (
	"github.com/VladimirovArtem/template/api"
	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/model"
)

func toInput(body api.CreateTripJSONRequestBody) model.CreateTripInput {
	return model.CreateTripInput{
		UserID:     body.UserId,
		DriverID:   body.DriverId,
		Price:      body.Price,
		StartPoint: trip.Coordinates{Latitude: body.StartPoint.Latitude, Longitude: body.StartPoint.Longitude},
		EndPoint:   trip.Coordinates{Latitude: body.EndPoint.Latitude, Longitude: body.EndPoint.Longitude},
	}
}
