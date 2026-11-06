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
	"github.com/VladimirovArtem/template/internal/usecase/trip/create"
)

type transactionContextKey struct{}

func TestExecute(t *testing.T) {
	t.Parallel()
	writeErr := errors.New("write failed")
	historyErr := errors.New("history failed")
	txErr := errors.New("transaction failed")
	for _, tt := range []struct {
		name       string
		invalid    bool
		insertErr  error
		historyErr error
		beginErr   error
		endErr     error
		wantErr    error
	}{
		{name: "success"},
		{name: "invalid input", invalid: true, wantErr: trip.ErrInvalidInput},
		{name: "driver busy", insertErr: fmt.Errorf("insert: %w", trip.ErrDriverBusy), wantErr: trip.ErrDriverBusy},
		{name: "insert error", insertErr: writeErr, wantErr: writeErr},
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
			id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
			input := model.CreateTripInput{
				UserID:     uuid.MustParse("22222222-2222-4222-8222-222222222222"),
				DriverID:   uuid.MustParse("33333333-3333-4333-8333-333333333333"),
				StartPoint: trip.Coordinates{Latitude: -90, Longitude: -180},
				EndPoint:   trip.Coordinates{Latitude: 90, Longitude: 180},
				Price:      0,
			}
			proposed := trip.Trip{
				ID: id, UserID: input.UserID, DriverID: input.DriverID,
				StartPoint: input.StartPoint, EndPoint: input.EndPoint, Price: input.Price,
				Status: trip.StatusActive, StartedAt: now.UTC(),
			}
			created := proposed
			created.CreatedAt, created.UpdatedAt = now.UTC(), now.UTC()
			switch {
			case tt.invalid:
				input.UserID = uuid.Nil
			case tt.beginErr != nil:
				tx.EXPECT().Do(gomock.Eq(ctx), gomock.Any()).Return(tt.beginErr)
			default:
				tx.EXPECT().Do(gomock.Eq(ctx), gomock.Any()).DoAndReturn(func(_ context.Context, fn func(context.Context) error) error {
					callbackErr := fn(txCtx)
					wantCallbackErr := tt.insertErr
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
				insert := repo.EXPECT().Insert(gomock.Eq(txCtx), proposed).Return(created, tt.insertErr)
				if tt.insertErr == nil {
					repo.EXPECT().AddHistory(gomock.Eq(txCtx), model.TripHistory{TripID: id, ToStatus: trip.StatusActive}).
						After(insert).Return(tt.historyErr)
				}
			}
			if tt.wantErr == nil {
				observer.EXPECT().Created(gomock.Eq(ctx), id)
			}
			got, err := create.New(repo, tx, func() uuid.UUID { return id }, func() time.Time { return now }, observer).Execute(ctx, input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Execute() error = %v, want %v", err, tt.wantErr)
			}
			if tt.invalid {
				var validationErr *trip.ValidationError
				if !errors.As(err, &validationErr) || validationErr.Field != "user_id" || validationErr.Reason == "" {
					t.Fatalf("validation error details lost: %v", err)
				}
			}
			want := created
			if tt.wantErr != nil {
				want = trip.Trip{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Execute() = %+v, want %+v", got, want)
			}
		})
	}
}
