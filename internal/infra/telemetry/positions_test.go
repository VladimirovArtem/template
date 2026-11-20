package telemetry_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/mock/gomock"

	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/infra/telemetry"
	"github.com/VladimirovArtem/template/internal/model"
	positioncreate "github.com/VladimirovArtem/template/internal/usecase/position/create"
	"github.com/VladimirovArtem/template/internal/worker/positions"
)

func TestSavedPositionCancelsRealQueueOnlyAfterCommit(t *testing.T) {
	t.Parallel()
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit failed", true: "committed"}[committed], func(t *testing.T) {
			t.Parallel()
			traces, meters, reader, _ := memoryTelemetry(t)
			events, err := telemetry.NewPositions(meters)
			if err != nil {
				t.Fatal(err)
			}
			ctrl := gomock.NewController(t)
			repo, tx, gateway := NewMockpositionRepository(ctrl), NewMocktxManager(ctrl), NewMockpositionGateway(ctrl)
			pool, err := positions.New(gateway, repo, 1, 2, time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)), events, traces.Tracer("test"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := pool.Shutdown(ctx); err != nil {
					t.Error(err)
				}
			})
			if err := telemetry.RegisterPositionState(meters, pool); err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			id, driver := uuid.New(), uuid.New()
			at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
			pool.Put(ctx, model.StaleTrip{ID: id, DriverID: driver, ReferenceAt: at.Add(-time.Hour)})
			created := trip.Position{ID: 42, TripID: id, RecordedAt: at}
			repo.EXPECT().UpdateLastPositionAt(gomock.Any(), id, at).Return(nil)
			repo.EXPECT().InsertPosition(gomock.Any(), trip.Position{TripID: id, RecordedAt: at}).Return(created, nil)
			commitErr := errors.New("commit failed")
			tx.EXPECT().Do(gomock.Any(), gomock.Any()).DoAndReturn(func(txCtx context.Context, callback func(context.Context) error) error {
				if err := callback(txCtx); err != nil {
					return err
				}
				var before metricdata.ResourceMetrics
				if err := reader.Collect(ctx, &before); err != nil {
					t.Fatal(err)
				}
				pending, _ := pool.Snapshot()
				if pending != 1 || positionCounter(before, "tripgo.positions.saved", "") != 0 || positionCounter(before, "tripgo.position_requests", "cancelled") != 0 {
					t.Fatal("external effects ran inside Do")
				}
				if !committed {
					return commitErr
				}
				return nil
			})
			_, err = positioncreate.New(repo, tx, func() time.Time { return at }, pool, events).Execute(ctx, model.CreatePositionInput{TripID: id, RecordedAt: at})
			if committed && err != nil || !committed && !errors.Is(err, commitErr) {
				t.Fatal(err)
			}
			pending, _ := pool.Snapshot()
			if committed {
				if pending != 0 || pool.Cancel(ctx, id) {
					t.Fatal("committed position did not cancel exactly once")
				}
			} else {
				if pending != 1 {
					t.Fatal("failed commit canceled work")
				}
				gateway.EXPECT().RequestPosition(gomock.Any(), gomock.Any()).Return(nil)
				repo.EXPECT().StillStale(gomock.Any(), gomock.Any()).Return(true, nil)
			}
			if err := pool.Start(ctx); err != nil {
				t.Fatal(err)
			}
			if err := pool.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			var output metricdata.ResourceMetrics
			if err := reader.Collect(ctx, &output); err != nil {
				t.Fatal(err)
			}
			want := int64(0)
			if committed {
				want = 1
			}
			if positionCounter(output, "tripgo.positions.saved", "") != want || positionCounter(output, "tripgo.position_requests", "cancelled") != want {
				t.Fatal("post-commit metrics are inconsistent")
			}
			durationCount := positionDurationCount(output)
			if committed && durationCount != 0 || !committed && durationCount != 1 {
				t.Fatal("queued cancellation fabricated HTTP duration")
			}
		})
	}
}

func positionCounter(data metricdata.ResourceMetrics, name, result string) int64 {
	var value int64
	for _, scope := range data.ScopeMetrics {
		for _, item := range scope.Metrics {
			if item.Name != name {
				continue
			}
			for _, point := range item.Data.(metricdata.Sum[int64]).DataPoints {
				attr, _ := point.Attributes.Value("result")
				if attr.AsString() == result {
					value += point.Value
				}
			}
		}
	}
	return value
}

func positionDurationCount(output metricdata.ResourceMetrics) uint64 {
	var durationCount uint64
	for _, scope := range output.ScopeMetrics {
		for _, item := range scope.Metrics {
			if item.Name == "tripgo.position_requests.duration" {
				for _, point := range item.Data.(metricdata.Histogram[float64]).DataPoints {
					durationCount += point.Count
				}
			}
		}
	}
	return durationCount
}
