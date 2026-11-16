package telemetry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/mock/gomock"

	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/infra/telemetry"
	"github.com/VladimirovArtem/template/internal/logging"
	"github.com/VladimirovArtem/template/internal/model"
	"github.com/VladimirovArtem/template/internal/usecase/trip/create"
	"github.com/VladimirovArtem/template/internal/usecase/trip/finish"
)

type txContextKey struct{}

func TestBusinessSignalsAfterCommit(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"create", "finish"} {
		for _, committed := range []bool{true, false} {
			t.Run(operation+"/committed="+map[bool]string{true: "true", false: "false"}[committed], func(t *testing.T) {
				t.Parallel()
				traces, meters, reader, _ := memoryTelemetry(t)
				var logs bytes.Buffer
				logger := slog.New(logging.Handler{Handler: slog.NewJSONHandler(&logs, nil)}).With("service", testServiceName)
				events, err := telemetry.NewTrips(meters, logger)
				if err != nil {
					t.Fatal(err)
				}
				ctrl := gomock.NewController(t)
				repo, tx := NewMocktripRepository(ctrl), NewMocktxManager(ctrl)
				ctx, span := traces.Tracer("test").Start(t.Context(), "commit")
				defer span.End()
				txCtx := context.WithValue(ctx, txContextKey{}, t.Name())
				commitErr := errors.New("commit failed")
				metricName, message := "tripgo.trips.created", "trip created"
				if operation == "finish" {
					metricName, message = "tripgo.trips.completed", "trip completed"
				}
				tx.EXPECT().Do(gomock.Eq(ctx), gomock.Any()).DoAndReturn(func(_ context.Context, callback func(context.Context) error) error {
					if err := callback(txCtx); err != nil {
						return err
					}
					var before metricdata.ResourceMetrics
					if err := reader.Collect(ctx, &before); err != nil {
						t.Fatal(err)
					}
					if businessCount(t, before, metricName) != 0 {
						t.Fatal("business counter updated before Do returned")
					}
					if committed {
						return nil
					}
					return commitErr
				})
				at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
				id := uuid.New()
				input := model.CreateTripInput{UserID: uuid.New(), DriverID: uuid.New()}
				active := trip.Trip{ID: id, UserID: input.UserID, DriverID: input.DriverID, Status: trip.StatusActive, StartedAt: at}
				if operation == "create" {
					repo.EXPECT().Insert(gomock.Eq(txCtx), active).Return(active, nil)
					repo.EXPECT().AddHistory(gomock.Eq(txCtx), model.TripHistory{TripID: id, ToStatus: trip.StatusActive}).Return(nil)
					_, err = create.New(repo, tx, func() uuid.UUID { return id }, func() time.Time { return at }, events).Execute(ctx, input)
				} else {
					completed := active
					completed.Status, completed.FinishedAt, completed.UpdatedAt = trip.StatusCompleted, &at, at
					from := trip.StatusActive
					repo.EXPECT().Get(gomock.Eq(txCtx), id).Return(active, nil)
					repo.EXPECT().Finish(gomock.Eq(txCtx), completed).Return(completed, nil)
					repo.EXPECT().AddHistory(gomock.Eq(txCtx), model.TripHistory{TripID: id, FromStatus: &from, ToStatus: trip.StatusCompleted}).Return(nil)
					_, err = finish.New(repo, tx, func() time.Time { return at }, events).Execute(ctx, id)
				}
				if committed && err != nil || !committed && !errors.Is(err, commitErr) {
					t.Fatalf("usecase result: %v", err)
				}
				var output metricdata.ResourceMetrics
				if err := reader.Collect(ctx, &output); err != nil {
					t.Fatal(err)
				}
				count := businessCount(t, output, metricName)
				if !committed {
					if count != 0 || logs.Len() != 0 {
						t.Fatalf("failed commit emitted signals: count=%d, logs=%s", count, &logs)
					}
					return
				}
				var record map[string]any
				if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
					t.Fatal(err)
				}
				if count != 1 || record["msg"] != message || record["trace_id"] != span.SpanContext().TraceID().String() || record["trip_id"] != id.String() {
					t.Fatalf("committed signals: count=%d, logs=%s", count, &logs)
				}
			})
		}
	}
}

func TestActiveTripGauge(t *testing.T) {
	t.Parallel()
	queryErr := errors.New("count unavailable")
	for _, tt := range []struct {
		name  string
		count int64
		err   error
	}{
		{name: "active trips", count: 3},
		{name: "empty database"},
		{name: "query error", err: queryErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, meters, reader, _ := memoryTelemetry(t)
			repo := NewMockactiveTrips(gomock.NewController(t))
			repo.EXPECT().CountActive(gomock.Any()).DoAndReturn(func(ctx context.Context) (int64, error) {
				if _, exists := ctx.Deadline(); !exists {
					t.Fatal("gauge query has no timeout")
				}
				return tt.count, tt.err
			})
			if err := telemetry.RegisterActiveTrips(meters, repo, time.Second); err != nil {
				t.Fatal(err)
			}
			var output metricdata.ResourceMetrics
			err := reader.Collect(t.Context(), &output)
			if tt.err != nil {
				if !errors.Is(err, queryErr) {
					t.Fatalf("collection error: %v", err)
				}
				for _, scope := range output.ScopeMetrics {
					for _, item := range scope.Metrics {
						if item.Name == "tripgo.trips.active" && len(item.Data.(metricdata.Gauge[int64]).DataPoints) != 0 {
							t.Fatal("failed query fabricated a gauge value")
						}
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			item := findMetric(t, output, "tripgo.trips.active")
			gauge := item.Data.(metricdata.Gauge[int64])
			if item.Unit != "{trip}" || len(gauge.DataPoints) != 1 || gauge.DataPoints[0].Value != tt.count || gauge.DataPoints[0].Attributes.Len() != 0 {
				t.Fatalf("gauge: %+v", item)
			}
		})
	}
}

func businessCount(t *testing.T, output metricdata.ResourceMetrics, name string) int64 {
	t.Helper()
	var count int64
	for _, scope := range output.ScopeMetrics {
		for _, item := range scope.Metrics {
			if item.Name != name {
				continue
			}
			sum := item.Data.(metricdata.Sum[int64])
			if !sum.IsMonotonic || item.Unit != "{trip}" {
				t.Fatal("business counter has wrong type or unit")
			}
			for _, point := range sum.DataPoints {
				if point.Attributes.Len() != 0 {
					t.Fatal("business counter has labels")
				}
				count += point.Value
			}
		}
	}
	return count
}
