package trip_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/VladimirovArtem/template/internal/domain/trip"
)

func TestTripFinish(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, time.November, 1, 9, 0, 0, 0, time.UTC)
	finishedAt := startedAt.Add(time.Hour)
	for _, tt := range []struct {
		name       string
		status     trip.Status
		finishedAt *time.Time
		wantErr    error
	}{
		{name: "active", status: trip.StatusActive},
		{name: "completed", status: trip.StatusCompleted, finishedAt: &finishedAt, wantErr: trip.ErrCompleted},
		{name: "unknown status", status: "unknown", wantErr: trip.ErrCompleted},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			candidate := trip.Trip{
				ID:     uuid.MustParse("11111111-1111-4111-8111-111111111111"),
				Status: tt.status, StartedAt: startedAt, FinishedAt: tt.finishedAt, UpdatedAt: startedAt,
			}
			before := candidate
			var previousFinish time.Time
			if candidate.FinishedAt != nil {
				previousFinish = *candidate.FinishedAt
			}
			err := candidate.Finish(finishedAt)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Finish() = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				if !reflect.DeepEqual(candidate, before) {
					t.Fatalf("failed Finish modified trip: %+v", candidate)
				}
				if candidate.FinishedAt != nil && !candidate.FinishedAt.Equal(previousFinish) {
					t.Fatal("failed Finish changed completion time")
				}
				return
			}
			want := before
			want.Status, want.FinishedAt, want.UpdatedAt = trip.StatusCompleted, &finishedAt, finishedAt
			if !reflect.DeepEqual(candidate, want) {
				t.Fatalf("finished trip = %+v, want %+v", candidate, want)
			}
			completed := candidate
			if err := candidate.Finish(finishedAt.Add(time.Hour)); !errors.Is(err, trip.ErrCompleted) {
				t.Fatalf("repeated Finish() = %v", err)
			}
			if !reflect.DeepEqual(candidate, completed) || candidate.FinishedAt != completed.FinishedAt || !candidate.FinishedAt.Equal(finishedAt) {
				t.Fatal("repeated Finish changed completed trip")
			}
		})
	}
}
