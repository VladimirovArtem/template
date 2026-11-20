package positions

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/VladimirovArtem/template/internal/logging"
	"github.com/VladimirovArtem/template/internal/model"
)

type ScanConfig struct {
	Interval   time.Duration
	StaleAfter time.Duration
	BatchSize  int
}

type scanner struct {
	repo     staleRepository
	pool     *Pool
	config   ScanConfig
	now      func() time.Time
	observer scanObserver
	tracer   trace.Tracer
	logger   *slog.Logger
}

func (s *scanner) run(ctx context.Context, ticker Ticker) {
	defer ticker.Stop()
	s.logger.InfoContext(ctx, "position scanner started")
	defer s.logger.InfoContext(context.WithoutCancel(ctx), "position scanner stopped")
	ticks := ticker.Ticks()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ticks:
			if !ok || ctx.Err() != nil {
				return
			}
			s.scan(ctx)
		}
	}
}

func (s *scanner) scan(ctx context.Context) {
	ctx, span := s.tracer.Start(ctx, "positions.check", trace.WithNewRoot())
	defer span.End()
	rows := 0
	defer func() { s.observer.RowsRead(ctx, rows) }()
	checkedAt := s.now().UTC()
	seen := make(map[uuid.UUID]struct{})
	var cursor *model.StaleTripCursor
	for ctx.Err() == nil {
		batch, err := s.repo.ListStaleTrips(ctx, checkedAt, s.config.StaleAfter, s.config.BatchSize, cursor)
		if err != nil {
			if ctx.Err() == nil {
				span.SetStatus(codes.Error, "stale trip query failed")
				span.RecordError(logging.SafeError{Cause: err})
				s.logger.LogAttrs(ctx, slog.LevelError, "stale trip query failed", logging.ErrorAttrs(err)...)
			}
			return
		}
		rows += len(batch)
		for _, trip := range batch {
			if ctx.Err() != nil {
				return
			}
			if _, ok := seen[trip.ID]; ok {
				s.observer.Checked(ctx, string(Duplicate))
				continue
			}
			seen[trip.ID] = struct{}{}
			if !s.put(ctx, trip) {
				return
			}
		}
		if len(batch) < s.config.BatchSize {
			return
		}
		last := batch[len(batch)-1]
		cursor = &model.StaleTripCursor{ReferenceAt: last.ReferenceAt, ID: last.ID}
	}
}

func (s *scanner) put(ctx context.Context, trip model.StaleTrip) bool {
	result := s.pool.Put(ctx, trip)
	if result.Status == Closed {
		return false
	}
	s.observer.Checked(ctx, string(result.Status))
	if result.Evicted != nil {
		s.observer.Checked(ctx, string(Dropped))
		s.logger.WarnContext(ctx, "position queue evicted trip", "trip_id", result.Evicted.ID)
		return false
	}
	if result.Status == Dropped {
		s.logger.WarnContext(ctx, "position queue full", "trip_id", trip.ID)
		return false
	}
	return true
}

type systemTicker struct{ *time.Ticker }

func (t systemTicker) Ticks() <-chan time.Time { return t.C }

func NewTicker(interval time.Duration) Ticker { return systemTicker{time.NewTicker(interval)} }
