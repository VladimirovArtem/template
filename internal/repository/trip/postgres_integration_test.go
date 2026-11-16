//go:build integration

package trip_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/VladimirovArtem/template/internal/infra/telemetry"
)

const (
	postgresImage  = "postgres:17.6"
	testDatabase   = "tripgo_test"
	startupTimeout = 2 * time.Minute
	queryTimeout   = 10 * time.Second
	testTimeout    = 30 * time.Second
	cleanupTimeout = 30 * time.Second
)

var (
	testPool   *pgxpool.Pool
	testEvents *telemetry.Trips
)

func TestMain(m *testing.M) {
	os.Exit(runIntegrationTests(m))
}

func runIntegrationTests(m *testing.M) (code int) {
	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: postgresImage,
			Env: map[string]string{
				"POSTGRES_USER":     testDatabase,
				"POSTGRES_PASSWORD": testDatabase,
				"POSTGRES_DB":       testDatabase,
			},
			ExposedPorts: []string{"5432/tcp"},
			Labels:       map[string]string{"tripgo.integration": "true"},
			WaitingFor: wait.ForAll(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
				wait.ForListeningPort("5432/tcp"),
			).WithDeadline(startupTimeout),
		},
		Started: true,
	})
	if container != nil {
		defer func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), cleanupTimeout)
			defer cleanupCancel()
			if err := container.Terminate(cleanupCtx); err != nil {
				fmt.Fprintln(os.Stderr, "remove temporary PostgreSQL:", err)
				code = 1
			}
		}()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "start temporary PostgreSQL (Docker must be available):", err)
		return 1
	}
	endpoint, err := container.PortEndpoint(ctx, "5432/tcp", "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "temporary PostgreSQL endpoint:", err)
		return 1
	}
	dsn := (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(testDatabase, testDatabase),
		Host:     endpoint,
		Path:     "/" + testDatabase,
		RawQuery: "sslmode=disable",
	}).String()
	if err := migrate(ctx, dsn); err != nil {
		fmt.Fprintln(os.Stderr, "migrate temporary PostgreSQL:", err)
		return 1
	}
	testPool, err = pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create temporary PostgreSQL pool:", err)
		return 1
	}
	defer testPool.Close()
	if err := testPool.Ping(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "ping temporary PostgreSQL:", err)
		return 1
	}
	testEvents, err = telemetry.NewTrips(noop.NewMeterProvider(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "initialize test trip observer:", err)
		return 1
	}
	return m.Run()
}

func migrate(ctx context.Context, dsn string) (result error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer func() {
		if err := db.Close(); err != nil && result == nil {
			result = err
		}
	}()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../../migrations"))
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	return err
}
