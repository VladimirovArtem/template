//go:generate go tool mockgen -source contract.go -destination mocks_test.go -package positions

package positions

import (
	"context"
	"time"

	"github.com/VladimirovArtem/template/internal/model"
)

type pushGateway interface {
	RequestPosition(context.Context, model.PositionRequest) error
}

type staleRepository interface {
	ListStaleTrips(context.Context, time.Time, time.Duration, int, *model.StaleTripCursor) ([]model.StaleTrip, error)
}

type candidateChecker interface {
	StillStale(context.Context, model.StaleTrip) (bool, error)
}

type requestObserver interface {
	Requested(context.Context, string, time.Duration)
	Canceled(context.Context)
}

type scanObserver interface {
	Checked(context.Context, string)
	RowsRead(context.Context, int)
}

type Ticker interface {
	Ticks() <-chan time.Time
	Stop()
}
