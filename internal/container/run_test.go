package container

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/goleak"
	"go.uber.org/mock/gomock"

	"github.com/VladimirovArtem/template/internal/domain/trip"
	"github.com/VladimirovArtem/template/internal/infra/telemetry"
	"github.com/VladimirovArtem/template/internal/model"
	positioncreate "github.com/VladimirovArtem/template/internal/usecase/position/create"
	"github.com/VladimirovArtem/template/internal/worker/positions"
)

const containerTestBudget = 20 * time.Second

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

type runtimeFixture struct {
	ctx            context.Context
	container      *Container
	pool           *positions.Pool
	worker         *positions.Worker
	scanner        *MockscanRepository
	gateway        *MockpositionGateway
	events         *telemetry.Positions
	sdk            *MocktelemetryShutdown
	push           *MockpushClient
	database       *MockdatabasePool
	ticks          chan time.Time
	address        chan string
	scannerStopped chan struct{}
	signal         context.CancelFunc
}

func newRuntimeFixture(t *testing.T) *runtimeFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), containerTestBudget)
	t.Cleanup(cancel)
	f := &runtimeFixture{ctx: ctx, ticks: make(chan time.Time, 1), address: make(chan string, 1), scannerStopped: make(chan struct{})}
	ctrl := gomock.NewController(t)
	f.scanner, f.gateway = NewMockscanRepository(ctrl), NewMockpositionGateway(ctrl)
	f.sdk, f.push, f.database = NewMocktelemetryShutdown(ctrl), NewMockpushClient(ctrl), NewMockdatabasePool(ctrl)
	events, err := telemetry.NewPositions(metricnoop.NewMeterProvider())
	if err != nil {
		t.Fatal(err)
	}
	f.events = events
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	f.scanner.EXPECT().StillStale(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	f.pool, err = positions.New(f.gateway, f.scanner, 1, 2, containerTestBudget, logger, events, tracenoop.NewTracerProvider().Tracer("test"))
	if err != nil {
		t.Fatal(err)
	}
	ticker := NewMockscanTicker(ctrl)
	ticker.EXPECT().Ticks().Return(f.ticks).Times(1)
	ticker.EXPECT().Stop().Do(func() { close(f.scannerStopped) }).Times(1)
	f.worker, err = positions.NewWorker(f.scanner, f.pool, positions.ScanConfig{Interval: time.Second, StaleAfter: time.Minute, BatchSize: 100},
		func(time.Duration) positions.Ticker { return ticker }, time.Now, events, tracenoop.NewTracerProvider().Tracer("test"), logger)
	if err != nil {
		t.Fatal(err)
	}
	f.container = &Container{
		server: &http.Server{
			Addr: "127.0.0.1:0", ReadHeaderTimeout: time.Second,
			BaseContext: func(listener net.Listener) context.Context {
				f.address <- listener.Addr().String()
				return context.WithoutCancel(ctx)
			},
		},
		logger: logger, shutdownTimeout: containerTestBudget, positions: f.worker, telemetry: f.sdk, push: f.push, pool: f.database,
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), containerTestBudget)
		defer cancel()
		_ = f.container.shutdown(cleanup)
		select {
		case <-f.container.shutdownDone:
		case <-cleanup.Done():
			t.Error("runtime cleanup did not finish")
		}
	})
	return f
}

func awaitRuntime(t *testing.T, ctx context.Context, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func startRuntime(t *testing.T, f *runtimeFixture) (string, <-chan error) {
	t.Helper()
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(f.ctx)
	f.signal = cancel
	t.Cleanup(cancel)
	go func() { done <- f.container.Run(ctx) }()
	select {
	case addr := <-f.address:
		return "http://" + addr, done
	case <-f.ctx.Done():
		t.Fatal(f.ctx.Err())
	}
	return "", done
}

func expectResourceClose(t *testing.T, f *runtimeFixture) {
	t.Helper()
	first := f.sdk.EXPECT().Shutdown(gomock.Any()).DoAndReturn(func(context.Context) error {
		select {
		case <-f.worker.Done():
		default:
			t.Error("SDK stopped before background work")
		}
		pending, active := f.pool.Snapshot()
		if pending != 0 || active != 0 {
			t.Error("SDK observed unfinished background work")
		}
		return nil
	})
	second := f.push.EXPECT().CloseIdleConnections().After(first)
	f.database.EXPECT().Close().After(second)
}

func TestShutdownWaitsForHTTPCommitAndCancel(t *testing.T) {
	f := newRuntimeFixture(t)
	expectResourceClose(t, f)
	ctrl := gomock.NewController(t)
	repo, tx := NewMockpositionRepository(ctrl), NewMocktxManager(ctrl)
	at := time.Now().UTC().Truncate(time.Microsecond)
	primer, target := uuid.New(), uuid.New()
	f.pool.Put(f.ctx, model.StaleTrip{ID: primer, DriverID: uuid.New(), ReferenceAt: at.Add(-time.Hour)})
	pushEntered, pushRelease := make(chan context.Context, 1), make(chan struct{})
	f.gateway.EXPECT().RequestPosition(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, input model.PositionRequest) error {
		if input.TripID != primer {
			t.Error("canceled trip was pushed")
		}
		pushEntered <- ctx
		select {
		case <-pushRelease:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	commitEntered, commitRelease, saved := make(chan struct{}), make(chan struct{}), make(chan struct{})
	repo.EXPECT().UpdateLastPositionAt(gomock.Any(), target, at).Return(nil)
	repo.EXPECT().InsertPosition(gomock.Any(), trip.Position{TripID: target, RecordedAt: at}).Return(trip.Position{ID: 1, TripID: target, RecordedAt: at}, nil)
	tx.EXPECT().Do(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, callback func(context.Context) error) error {
		if err := callback(ctx); err != nil {
			return err
		}
		close(commitEntered)
		select {
		case <-commitRelease:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	saver := positioncreate.New(repo, tx, func() time.Time { return at }, f.pool, f.events)
	f.container.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := saver.Execute(r.Context(), model.CreatePositionInput{TripID: target, RecordedAt: at}); err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		close(saved)
		w.WriteHeader(http.StatusCreated)
	})
	url, runDone := startRuntime(t, f)
	var pushCtx context.Context
	select {
	case pushCtx = <-pushEntered:
	case <-f.ctx.Done():
		t.Fatal(f.ctx.Err())
	}
	f.pool.Put(f.ctx, model.StaleTrip{ID: target, DriverID: uuid.New(), ReferenceAt: at})
	client := &http.Client{Transport: &http.Transport{}, Timeout: containerTestBudget}
	t.Cleanup(client.CloseIdleConnections)
	response := make(chan error, 1)
	go func() {
		r, err := client.Get(url)
		if err == nil {
			_ = r.Body.Close()
			if r.StatusCode != http.StatusCreated {
				err = errors.New("position HTTP failed")
			}
		}
		response <- err
	}()
	awaitRuntime(t, f.ctx, commitEntered)
	shutdownStarted := make(chan struct{})
	f.container.server.RegisterOnShutdown(func() { close(shutdownStarted) })
	f.signal()
	awaitRuntime(t, f.ctx, shutdownStarted)
	queryEntered := make(chan struct{})
	f.scanner.EXPECT().ListStaleTrips(gomock.Any(), gomock.Any(), time.Minute, 100, nil).DoAndReturn(func(ctx context.Context, _ time.Time, _ time.Duration, _ int, _ *model.StaleTripCursor) ([]model.StaleTrip, error) {
		if ctx.Err() != nil {
			t.Error("scanner canceled while HTTP was active")
		}
		close(queryEntered)
		return nil, nil
	})
	f.ticks <- at
	awaitRuntime(t, f.ctx, queryEntered)
	close(commitRelease)
	awaitRuntime(t, f.ctx, saved)
	select {
	case err := <-response:
		if err != nil {
			t.Fatal(err)
		}
	case <-f.ctx.Done():
		t.Fatal(f.ctx.Err())
	}
	awaitRuntime(t, f.ctx, f.scannerStopped)
	if pushCtx.Err() != nil {
		t.Fatal("scanner stop canceled a draining Push")
	}
	if pending, _ := f.pool.Snapshot(); pending != 0 {
		t.Fatal("saved coordinate did not cancel waiting task")
	}
	close(pushRelease)
	for _, done := range []<-chan error{runDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-f.ctx.Done():
			t.Fatal(f.ctx.Err())
		}
	}
	if err := f.container.shutdown(f.ctx); err != nil {
		t.Fatal(err)
	}
	t.Log("HTTP commit and real queue Cancel finished before scanner stopped; Push drained before SDK/client/database close")
}

func TestShutdownBudgetCancelsScannerAndPushBeforeClosingDependencies(t *testing.T) {
	f := newRuntimeFixture(t)
	expectResourceClose(t, f)
	primer := uuid.New()
	f.pool.Put(f.ctx, model.StaleTrip{ID: primer, DriverID: uuid.New(), ReferenceAt: time.Now()})
	entered, canceled, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	f.gateway.EXPECT().RequestPosition(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ model.PositionRequest) error {
		close(entered)
		<-ctx.Done()
		close(canceled)
		select {
		case <-finish:
		case <-f.ctx.Done():
		}
		return ctx.Err()
	})
	f.container.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	_, runDone := startRuntime(t, f)
	awaitRuntime(t, f.ctx, entered)
	queryEntered, queryCanceled := make(chan struct{}), make(chan struct{})
	f.scanner.EXPECT().ListStaleTrips(gomock.Any(), gomock.Any(), time.Minute, 100, nil).DoAndReturn(func(ctx context.Context, _ time.Time, _ time.Duration, _ int, _ *model.StaleTripCursor) ([]model.StaleTrip, error) {
		close(queryEntered)
		<-ctx.Done()
		close(queryCanceled)
		return nil, ctx.Err()
	})
	f.ticks <- time.Now()
	awaitRuntime(t, f.ctx, queryEntered)
	budget, cancel := context.WithTimeout(f.ctx, 20*time.Millisecond)
	defer cancel()
	if err := f.container.shutdown(budget); !errors.Is(err, ErrShutdownTimeout) {
		t.Fatalf("shutdown result: %v", err)
	}
	awaitRuntime(t, f.ctx, queryCanceled)
	awaitRuntime(t, f.ctx, canceled)
	select {
	case <-f.worker.Done():
		t.Fatal("worker exited before gateway returned")
	default:
	}
	close(finish)
	awaitRuntime(t, f.ctx, f.container.shutdownDone)
	select {
	case err := <-runDone:
		if !errors.Is(err, ErrShutdownTimeout) {
			t.Fatalf("Run result: %v", err)
		}
	case <-f.ctx.Done():
		t.Fatal(f.ctx.Err())
	}
	t.Log("common shutdown deadline signaled scanner and Push cancellation; dependencies closed after worker exit")
}

func TestOccupiedPortNeverStartsBackground(t *testing.T) {
	ctrl := gomock.NewController(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	background := NewMockpositionWorker(ctrl)
	done := make(chan struct{})
	var once sync.Once
	background.EXPECT().Shutdown(gomock.Any()).DoAndReturn(func(context.Context) error { once.Do(func() { close(done) }); return nil })
	background.EXPECT().Done().Return((<-chan struct{})(done))
	container := &Container{
		server: &http.Server{Addr: listener.Addr().String(), ReadHeaderTimeout: time.Second}, positions: background,
		logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), shutdownTimeout: time.Second,
	}
	if err := container.Run(t.Context()); err == nil {
		t.Fatal("occupied port accepted")
	}
}
