package get_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/usecase/trip/get"
)

func TestExecute(t *testing.T) {
	t.Parallel()
	dependencyErr := errors.New("repository unavailable")
	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	for _, tt := range []struct {
		name    string
		status  trip.Status
		repoErr error
		wantErr error
	}{
		{name: "active trip", status: trip.StatusActive},
		{name: "completed trip", status: trip.StatusCompleted},
		{name: "missing trip", repoErr: fmt.Errorf("select: %w", trip.ErrNotFound), wantErr: trip.ErrNotFound},
		{name: "repository error", repoErr: dependencyErr, wantErr: dependencyErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			repo := NewMocktripRepository(ctrl)
			ctx := t.Context()
			found := trip.Trip{ID: id, Status: tt.status, Price: 1450}
			repo.EXPECT().Get(gomock.Eq(ctx), id).Return(found, tt.repoErr)
			got, err := get.New(repo).Execute(ctx, id)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Execute() error = %v, want %v", err, tt.wantErr)
			}
			want := found
			if tt.wantErr != nil {
				want = trip.Trip{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Execute() = %+v, want %+v", got, want)
			}
		})
	}
}
