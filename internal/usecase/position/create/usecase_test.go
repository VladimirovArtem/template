package create_test

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
	"github.com/VladimirovArtem/template/internal/usecase/position/create"
)

type transactionContextKey struct{}

func TestExecute(t *testing.T) {
	t.Parallel()
	writeErr := errors.New("write failed")
	txErr := errors.New("transaction failed")
	for _, tt := range []struct {
		name      string
		updateErr error
		insertErr error
		beginErr  error
		endErr    error
	}{
		{name: "success"},
		{name: "missing trip", updateErr: fmt.Errorf("select: %w", trip.ErrNotFound)},
		{name: "completed trip", updateErr: fmt.Errorf("update: %w", trip.ErrCompleted)},
		{name: "update error", updateErr: writeErr},
		{name: "insert error", insertErr: writeErr},
		{name: "begin error", beginErr: txErr},
		{name: "commit error", endErr: txErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			repo, tx := NewMockpositionRepository(ctrl), NewMocktxManager(ctrl)
			canceler, observer := NewMockpositionCanceler(ctrl), NewMockpositionObserver(ctrl)
			ctx := t.Context()
			txCtx := context.WithValue(ctx, transactionContextKey{}, t.Name())
			now := time.Date(2026, 11, 1, 12, 0, 0, 123456000, time.FixedZone("MSK", 3*60*60))
			input := model.CreatePositionInput{TripID: uuid.New(), Coordinates: trip.Coordinates{Latitude: 59.9, Longitude: 30.3}, RecordedAt: now.Add(time.Minute)}
			proposed := trip.Position{TripID: input.TripID, Coordinates: input.Coordinates, RecordedAt: input.RecordedAt.UTC()}
			created := proposed
			created.ID, created.CreatedAt = 42, now.UTC()
			wantErr := tt.beginErr
			if tt.beginErr != nil {
				tx.EXPECT().Do(gomock.Eq(ctx), gomock.Any()).Return(tt.beginErr)
			} else {
				wantErr = tt.updateErr
				if wantErr == nil {
					wantErr = tt.insertErr
				}
				callbackErr := wantErr
				if wantErr == nil {
					wantErr = tt.endErr
				}
				tx.EXPECT().Do(gomock.Eq(ctx), gomock.Any()).DoAndReturn(func(_ context.Context, fn func(context.Context) error) error {
					err := fn(txCtx)
					if !errors.Is(err, callbackErr) {
						t.Fatalf("transaction callback = %v, want %v", err, callbackErr)
					}
					if err != nil {
						return err
					}
					return tt.endErr
				})
				update := repo.EXPECT().UpdateLastPositionAt(gomock.Eq(txCtx), input.TripID, proposed.RecordedAt).Return(tt.updateErr)
				if tt.updateErr == nil {
					repo.EXPECT().InsertPosition(gomock.Eq(txCtx), proposed).After(update).Return(created, tt.insertErr)
				}
			}
			if wantErr == nil {
				cancel := canceler.EXPECT().Cancel(gomock.Eq(ctx), input.TripID).Return(true)
				observer.EXPECT().Saved(gomock.Eq(ctx)).After(cancel)
			}
			got, err := create.New(repo, tx, func() time.Time { return now }, canceler, observer).Execute(ctx, input)
			if !errors.Is(err, wantErr) {
				t.Fatalf("Execute() = %v, want %v", err, wantErr)
			}
			want := created
			if wantErr != nil {
				want = trip.Position{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("position = %+v, want %+v", got, want)
			}
		})
	}
}

func TestExecuteRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name   string
		change func(*model.CreatePositionInput)
	}{
		{"trip id", func(p *model.CreatePositionInput) { p.TripID = uuid.Nil }},
		{"coordinates", func(p *model.CreatePositionInput) { p.Coordinates.Latitude = 91 }},
		{"timestamp missing", func(p *model.CreatePositionInput) { p.RecordedAt = time.Time{} }},
		{"timestamp future", func(p *model.CreatePositionInput) { p.RecordedAt = now.Add(time.Minute + time.Nanosecond) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			input := model.CreatePositionInput{TripID: uuid.New(), RecordedAt: now}
			tt.change(&input)
			usecase := create.New(NewMockpositionRepository(ctrl), NewMocktxManager(ctrl), func() time.Time { return now }, NewMockpositionCanceler(ctrl), NewMockpositionObserver(ctrl))
			got, err := usecase.Execute(t.Context(), input)
			if !errors.Is(err, trip.ErrInvalidInput) || got != (trip.Position{}) {
				t.Fatalf("Execute() = %+v, %v", got, err)
			}
		})
	}
}
