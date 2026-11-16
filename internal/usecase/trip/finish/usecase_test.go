package finish_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/model"
	"github.com/VladimirovArtem/template/internal/usecase/trip/finish"
)

type transactionContextKey struct{}

func TestExecute(t *testing.T) {
	t.Parallel()
	readErr := errors.New("read failed")
	writeErr := errors.New("write failed")
	historyErr := errors.New("history failed")
	txErr := errors.New("transaction failed")
	for _, tt := range []struct {
		name       string
		completed  bool
		getErr     error
		finishErr  error
		historyErr error
		beginErr   error
		endErr     error
		wantErr    error
	}{
		{name: "success"},
		{name: "already completed", completed: true, wantErr: trip.ErrCompleted},
		{name: "missing trip", getErr: fmt.Errorf("select: %w", trip.ErrNotFound), wantErr: trip.ErrNotFound},
		{name: "read error", getErr: readErr, wantErr: readErr},
		{name: "update error", finishErr: writeErr, wantErr: writeErr},
		{name: "concurrent completion", finishErr: fmt.Errorf("update: %w", trip.ErrCompleted), wantErr: trip.ErrCompleted},
		{name: "trip disappeared", finishErr: trip.ErrNotFound, wantErr: trip.ErrNotFound},
		{name: "history error", historyErr: historyErr, wantErr: historyErr},
		{name: "transaction start error", beginErr: txErr, wantErr: txErr},
		{name: "transaction completion error", endErr: txErr, wantErr: txErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			repo, tx := NewMocktripRepository(ctrl), NewMocktxManager(ctrl)
			observer := NewMocktripObserver(ctrl)
			ctx := t.Context()
			txCtx := context.WithValue(ctx, transactionContextKey{}, t.Name())
			now := time.Date(2026, time.November, 1, 12, 0, 0, 0, time.FixedZone("MSK", 3*60*60))
			at := now.UTC()
			current := trip.Trip{
				ID:         uuid.MustParse("11111111-1111-4111-8111-111111111111"),
				UserID:     uuid.MustParse("22222222-2222-4222-8222-222222222222"),
				DriverID:   uuid.MustParse("33333333-3333-4333-8333-333333333333"),
				StartPoint: trip.Coordinates{Latitude: -90, Longitude: -180},
				EndPoint:   trip.Coordinates{Latitude: 90, Longitude: 180}, Price: 1450,
				Status: trip.StatusActive, StartedAt: at.Add(-time.Hour),
				CreatedAt: at.Add(-time.Hour), UpdatedAt: at.Add(-time.Hour),
			}
			if tt.completed {
				previousFinish := at.Add(-time.Minute)
				current.Status, current.FinishedAt, current.UpdatedAt = trip.StatusCompleted, &previousFinish, previousFinish
			}
			expectedSave := current
			expectedSave.Status, expectedSave.FinishedAt, expectedSave.UpdatedAt = trip.StatusCompleted, &at, at
			if tt.beginErr != nil {
				tx.EXPECT().Do(gomock.Eq(ctx), gomock.Any()).Return(tt.beginErr)
			} else {
				tx.EXPECT().Do(gomock.Eq(ctx), gomock.Any()).DoAndReturn(func(_ context.Context, fn func(context.Context) error) error {
					callbackErr := fn(txCtx)
					wantCallbackErr := tt.getErr
					if wantCallbackErr == nil && tt.completed {
						wantCallbackErr = trip.ErrCompleted
					}
					if wantCallbackErr == nil {
						wantCallbackErr = tt.finishErr
					}
					if wantCallbackErr == nil {
						wantCallbackErr = tt.historyErr
					}
					if !errors.Is(callbackErr, wantCallbackErr) {
						t.Fatalf("callback error = %v, want %v", callbackErr, wantCallbackErr)
					}
					if callbackErr != nil {
						return callbackErr
					}
					return tt.endErr
				})
				read := repo.EXPECT().Get(gomock.Eq(txCtx), current.ID).Return(current, tt.getErr)
				if tt.getErr == nil && !tt.completed {
					update := repo.EXPECT().Finish(gomock.Eq(txCtx), expectedSave).After(read).Return(expectedSave, tt.finishErr)
					if tt.finishErr == nil {
						from := trip.StatusActive
						repo.EXPECT().AddHistory(gomock.Eq(txCtx), model.TripHistory{
							TripID: current.ID, FromStatus: &from, ToStatus: trip.StatusCompleted,
						}).After(update).Return(tt.historyErr)
					}
				}
			}
			if tt.wantErr == nil {
				observer.EXPECT().Completed(gomock.Eq(ctx), current.ID)
			}
			got, err := finish.New(repo, tx, func() time.Time { return now }, observer).Execute(ctx, current.ID)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Execute() error = %v, want %v", err, tt.wantErr)
			}
			want := expectedSave
			if tt.wantErr != nil {
				want = trip.Trip{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Execute() = %+v, want %+v", got, want)
			}
		})
	}
}
