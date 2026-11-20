package list_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/usecase/position/list"
)

func TestExecute(t *testing.T) {
	t.Parallel()
	readErr := errors.New("read failed")
	for _, tt := range []struct {
		name    string
		status  trip.Status
		empty   bool
		getErr  error
		listErr error
	}{
		{name: "active route", status: trip.StatusActive},
		{name: "completed route", status: trip.StatusCompleted},
		{name: "empty route", status: trip.StatusActive, empty: true},
		{name: "empty completed route", status: trip.StatusCompleted, empty: true},
		{name: "missing trip", getErr: fmt.Errorf("select: %w", trip.ErrNotFound)},
		{name: "get error", getErr: readErr},
		{name: "route error", status: trip.StatusActive, listErr: readErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo := NewMocktripRepository(gomock.NewController(t))
			ctx, id := t.Context(), uuid.New()
			points := []trip.Position{{ID: 42, TripID: id, RecordedAt: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)}}
			if tt.empty {
				points = []trip.Position{}
			}
			get := repo.EXPECT().Get(gomock.Eq(ctx), id).Return(trip.Trip{ID: id, Status: tt.status}, tt.getErr)
			wantErr := tt.getErr
			if tt.getErr == nil {
				repo.EXPECT().ListPositions(gomock.Eq(ctx), id).After(get).Return(points, tt.listErr)
				wantErr = tt.listErr
			}
			got, err := list.New(repo).Execute(ctx, id)
			if !errors.Is(err, wantErr) {
				t.Fatalf("Execute() error = %v, want %v", err, wantErr)
			}
			if wantErr != nil {
				points = nil
			}
			if !reflect.DeepEqual(got, points) {
				t.Fatalf("route = %+v, want %+v", got, points)
			}
		})
	}
}

func TestExecuteRejectsZeroID(t *testing.T) {
	t.Parallel()
	repo := NewMocktripRepository(gomock.NewController(t))
	got, err := list.New(repo).Execute(t.Context(), uuid.Nil)
	if !errors.Is(err, trip.ErrInvalidInput) || got != nil {
		t.Fatalf("Execute() = %+v, %v", got, err)
	}
}
