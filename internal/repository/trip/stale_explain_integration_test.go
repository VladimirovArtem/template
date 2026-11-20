//go:build integration

package trip_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/VladimirovArtem/template/internal/model"
	repository "github.com/VladimirovArtem/template/internal/repository/trip"
)

const staleIndexName = "trips_active_reference_at_id_idx"

type stalePreparedQuery struct {
	Name         string
	SQL          string
	GenericPlans int64
	CustomPlans  int64
}

func TestStaleTripsExplain(t *testing.T) {
	ctx := testContext(t)
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	const staleAfter = time.Minute
	seedStaleExplainData(t, ctx, now)
	config := testPool.Config().Copy()
	config.MaxConns, config.MinConns = 1, 0
	config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	repo := repository.New(pool, queryTimeout)
	// Reuse the actual pgx prepared statements. These are ordinary batch calls;
	// no planner settings (including plan_cache_mode) are changed.
	for range 12 {
		if _, err := repo.ListStaleTrips(ctx, now, staleAfter, 1000, nil); err != nil {
			t.Fatal(err)
		}
	}
	first, err := repo.ListStaleTrips(ctx, now, staleAfter, 100, nil)
	if err != nil || len(first) != 100 {
		t.Fatalf("first batch: size=%d, error=%v", len(first), err)
	}
	firstQuery, firstCommand, firstPlan := explainStalePrepared(t, ctx, pool, []any{now, staleAfter.Microseconds(), 100})
	last := first[len(first)-1]
	cursor := &model.StaleTripCursor{ReferenceAt: last.ReferenceAt, ID: last.ID}
	for range 12 {
		if _, err := repo.ListStaleTrips(ctx, now, staleAfter, 1000, cursor); err != nil {
			t.Fatal(err)
		}
	}
	second, err := repo.ListStaleTrips(ctx, now, staleAfter, 100, cursor)
	if err != nil || len(second) != 100 || !staleTripLess(last, second[0]) {
		t.Fatalf("continuation: size=%d, error=%v", len(second), err)
	}
	secondArgs := []any{now, staleAfter.Microseconds(), cursor.ReferenceAt, cursor.ID, 100}
	secondQuery, secondCommand, secondPlan := explainStalePrepared(t, ctx, pool, secondArgs)
	var version, seqScan, planCache, indexSQL string
	for query, destination := range map[string]*string{
		"SHOW server_version": &version, "SHOW enable_seqscan": &seqScan, "SHOW plan_cache_mode": &planCache,
	} {
		if err := pool.QueryRow(ctx, query).Scan(destination); err != nil {
			t.Fatal(err)
		}
	}
	if seqScan != "on" || planCache != "auto" {
		t.Fatalf("unexpected planner settings: enable_seqscan=%s, plan_cache_mode=%s", seqScan, planCache)
	}
	if err := pool.QueryRow(ctx, "SELECT pg_get_indexdef($1::regclass)", staleIndexName).Scan(&indexSQL); err != nil {
		t.Fatal(err)
	}
	summary := fmt.Sprintf(`PostgreSQL: %s
Disposable database: tripgo_test (existing testcontainers suite)
Data: 9000 synthetic trips, 4500 matching position rows.
Six equally sized groups (1500 trips each):
  completed, old started_at, no position;
  completed, old started_at, fresh last_position_at;
  active, old started_at, no position;
  active, fresh started_at, no position;
  active, old started_at, old last_position_at;
  active, old started_at, fresh last_position_at.
Eligible: 3000 active stale trips; excluded: 3000 fresh active and 3000 completed.
ANALYZE trips and ANALYZE trip_positions executed after loading.
checked_at: %s; stale_after: %s; explained batch size: 100.
Cursor: last row returned by the actual first batch (see continuation-parameters.json).
pgx: QueryExecModeCacheStatement, one connection, default planner settings.
Warmup: 12 repository calls per query shape, batch size 1000; then a real batch of 100.
enable_seqscan=%s; plan_cache_mode=%s; no planner settings changed.
Prepared counters before EXPLAIN:
  first: custom_plans=%d, generic_plans=%d;
  continuation: custom_plans=%d, generic_plans=%d.
SQL read from pg_prepared_statements; EXPLAIN EXECUTE uses those same statements.
Index: %s
Only synthetic identifiers appear in the saved parameters.
Each test run replaces these ignored evidence files with its latest actual output.
`, version, now.Format(time.RFC3339Nano), staleAfter, seqScan, planCache,
		firstQuery.CustomPlans, firstQuery.GenericPlans, secondQuery.CustomPlans, secondQuery.GenericPlans, indexSQL)
	writeStaleExplainEvidence(t, map[string]string{
		"dataset.txt": summary,
		"first.sql":   firstQuery.SQL + ";\n", "first-explain.sql": firstCommand + ";\n", "first-plan.txt": firstPlan,
		"first-parameters.json": staleExplainParameters(t, []any{now, staleAfter.Microseconds(), 100}),
		"continuation.sql":      secondQuery.SQL + ";\n", "continuation-explain.sql": secondCommand + ";\n", "continuation-plan.txt": secondPlan,
		"continuation-parameters.json": staleExplainParameters(t, secondArgs),
	})
	t.Logf("first prepared plans: custom=%d generic=%d\n%s", firstQuery.CustomPlans, firstQuery.GenericPlans, firstPlan)
	t.Logf("continuation prepared plans: custom=%d generic=%d\n%s", secondQuery.CustomPlans, secondQuery.GenericPlans, secondPlan)
}

func seedStaleExplainData(t *testing.T, ctx context.Context, now time.Time) {
	t.Helper()
	owner := staleFixtureOwner(t)
	result, err := testPool.Exec(ctx, `INSERT INTO trips
		(id, user_id, driver_id, start_latitude, start_longitude, end_latitude, end_longitude,
		 price, status, started_at, finished_at, last_position_at)
		SELECT md5($1::text || ':trip:' || n)::uuid, $1::uuid, md5($1::text || ':driver:' || n)::uuid,
		 0, 0, 0, 0, 0,
		 CASE WHEN n % 6 IN (0, 1) THEN 'completed' ELSE 'active' END,
		 CASE WHEN n % 6 = 2 THEN $2::timestamptz - INTERVAL '2 hours' - (n / 6) * INTERVAL '1 second'
		      WHEN n % 6 = 3 THEN $2::timestamptz - INTERVAL '30 seconds'
		      ELSE $2::timestamptz - INTERVAL '1 day' END,
		 CASE WHEN n % 6 IN (0, 1) THEN $2::timestamptz - INTERVAL '1 second' END,
		 CASE WHEN n % 6 = 4 THEN $2::timestamptz - INTERVAL '1 hour' - (n / 6) * INTERVAL '1 second'
		      WHEN n % 6 IN (1, 5) THEN $2::timestamptz - INTERVAL '30 seconds' END
		FROM generate_series(1, 9000) AS series(n)`, owner.String(), now)
	if err != nil || result.RowsAffected() != 9000 {
		t.Fatalf("load EXPLAIN data: rows=%d, error=%v", result.RowsAffected(), err)
	}
	insertFixturePositions(t, ctx, owner)
	for _, query := range []string{"ANALYZE trips", "ANALYZE trip_positions"} {
		if _, err := testPool.Exec(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
}

func explainStalePrepared(t *testing.T, ctx context.Context, pool *pgxpool.Pool, args []any) (stalePreparedQuery, string, string) {
	t.Helper()
	var prepared stalePreparedQuery
	if err := pool.QueryRow(ctx, `SELECT name, statement, generic_plans, custom_plans FROM pg_prepared_statements
		WHERE statement LIKE 'SELECT id, driver_id,%FROM trips%'
		AND cardinality(parameter_types) = $1`, len(args)).Scan(&prepared.Name, &prepared.SQL, &prepared.GenericPlans, &prepared.CustomPlans); err != nil {
		t.Fatal(err)
	}
	if prepared.GenericPlans == 0 || prepared.CustomPlans == 0 {
		t.Fatalf("expected naturally reused custom and generic plans: %+v", prepared)
	}
	// PostgreSQL quotes the statement name and every synthetic argument. The
	// executed query comes from pg_prepared_statements, not a copy of its SQL.
	formatQuery := `SELECT format('EXPLAIN (ANALYZE, BUFFERS) EXECUTE %I(%L::timestamptz, %L::bigint, %L::bigint)',
		$1::text, $2::timestamptz, $3::bigint, $4::bigint)`
	if len(args) == 5 {
		formatQuery = `SELECT format('EXPLAIN (ANALYZE, BUFFERS) EXECUTE %I(%L::timestamptz, %L::bigint, %L::timestamptz, %L::uuid, %L::bigint)',
			$1::text, $2::timestamptz, $3::bigint, $4::timestamptz, $5::uuid, $6::bigint)`
	}
	var command string
	formatArgs := append([]any{prepared.Name}, args...)
	if err := pool.QueryRow(ctx, formatQuery, formatArgs...).Scan(&command); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(lines, "\n") + "\n"
	if !strings.Contains(plan, "Index Scan using "+staleIndexName) || strings.Contains(plan, "Seq Scan on trips") {
		t.Fatalf("unexpected stale trip plan:\n%s", plan)
	}
	return prepared, command, plan
}

func staleExplainParameters(t *testing.T, args []any) string {
	t.Helper()
	data, err := json.MarshalIndent(args, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}

func writeStaleExplainEvidence(t *testing.T, files map[string]string) {
	t.Helper()
	directory := filepath.Join("..", "..", "..", "bin", "lab03-evidence", "explain")
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
