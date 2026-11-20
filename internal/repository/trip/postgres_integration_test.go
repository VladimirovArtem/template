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
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	if _, err := provider.DownTo(ctx, 4); err != nil {
		return fmt.Errorf("roll back stale index migration: %w", err)
	}
	var staleIndexRemoved, positionsIntact bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass($1) IS NULL,
		to_regclass('trip_positions') IS NOT NULL AND EXISTS (
		SELECT 1 FROM information_schema.columns WHERE table_schema = 'public'
		AND table_name = 'trips' AND column_name = 'last_position_at')`, staleIndexName).Scan(&staleIndexRemoved, &positionsIntact); err != nil {
		return fmt.Errorf("check stale index down migration: %w", err)
	}
	if !staleIndexRemoved || !positionsIntact {
		return fmt.Errorf("stale index down migration: index removed=%t, positions intact=%t", staleIndexRemoved, positionsIntact)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("reapply stale index migration: %w", err)
	}
	// Exercise the new down migrations only on this disposable PostgreSQL.
	if _, err := provider.DownTo(ctx, 2); err != nil {
		return fmt.Errorf("roll back position migrations: %w", err)
	}
	var tableRemoved, columnRemoved, indexRemoved bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('trip_positions') IS NULL,
		NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public'
		AND table_name = 'trips' AND column_name = 'last_position_at'),
		to_regclass('trips_active_reference_at_id_idx') IS NULL`).Scan(&tableRemoved, &columnRemoved, &indexRemoved); err != nil {
		return fmt.Errorf("check position down migrations: %w", err)
	}
	if !tableRemoved || !columnRemoved || !indexRemoved {
		return fmt.Errorf("down migrations: table removed=%t, column removed=%t, stale index removed=%t", tableRemoved, columnRemoved, indexRemoved)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("reapply position migrations: %w", err)
	}
	return nil
}
