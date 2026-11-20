package telemetry

import "context"

type activeTrips interface {
	CountActive(context.Context) (int64, error)
}

type positionState interface {
	Snapshot() (pending, active int)
}
