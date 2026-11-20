package http

import (
	"net/http"

	"github.com/VladimirovArtem/template/api"
	"github.com/VladimirovArtem/template/internal/http/health"
	positioncreate "github.com/VladimirovArtem/template/internal/http/position/create"
	positionlist "github.com/VladimirovArtem/template/internal/http/position/list"
	"github.com/VladimirovArtem/template/internal/http/trip/create"
	"github.com/VladimirovArtem/template/internal/http/trip/finish"
	"github.com/VladimirovArtem/template/internal/http/trip/get"
)

type adapter struct {
	create         *create.Handler
	get            *get.Handler
	finish         *finish.Handler
	health         *health.Handler
	createPosition *positioncreate.Handler
	listPositions  *positionlist.Handler
}

var _ api.ServerInterface = (*adapter)(nil)

func (a *adapter) CreateTrip(w http.ResponseWriter, r *http.Request, _ api.CreateTripParams) {
	a.create.ServeHTTP(w, r)
}

func (a *adapter) GetTrip(w http.ResponseWriter, r *http.Request, id api.TripId) {
	a.get.Handle(w, r, id)
}

func (a *adapter) FinishTrip(w http.ResponseWriter, r *http.Request, id api.TripId) {
	a.finish.Handle(w, r, id)
}

func (a *adapter) CreateTripPosition(w http.ResponseWriter, r *http.Request, id api.TripId) {
	a.createPosition.Handle(w, r, id)
}

func (a *adapter) ListTripPositions(w http.ResponseWriter, r *http.Request, id api.TripId) {
	a.listPositions.Handle(w, r, id)
}

func (a *adapter) Health(w http.ResponseWriter, r *http.Request) {
	a.health.Health(w, r)
}

func (a *adapter) Ready(w http.ResponseWriter, r *http.Request) {
	a.health.Ready(w, r)
}
