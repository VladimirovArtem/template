package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/VladimirovArtem/template/internal/localenv"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	if len(os.Args) != 2 || (os.Args[1] != "up" && os.Args[1] != "down" && os.Args[1] != "status") {
		return fmt.Errorf("usage: go run ./scripts/migrate {up|down|status}")
	}
	if err := localenv.Load(ctx); err != nil {
		return err
	}
	dsn := os.Getenv("DATABASE_URL")
	if strings.TrimSpace(dsn) == "" {
		return fmt.Errorf("DATABASE_URL: required")
	}
	if _, err := pgxpool.ParseConfig(dsn); err != nil {
		return fmt.Errorf("DATABASE_URL: invalid PostgreSQL connection settings")
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "tool", "goose",
		"-env", "none", "-dir", "migrations", "-table", "goose_db_version",
		"-timeout", "1m", "-no-color", os.Args[1])
	cmd.Env = append(os.Environ(), "GOOSE_DRIVER=postgres", "GOOSE_DBSTRING="+dsn)
	cmd.WaitDelay = time.Second
	output, err := cmd.CombinedOutput()
	if err != nil {

		if ctx.Err() != nil {
			return fmt.Errorf("goose %s interrupted or timed out: %w", os.Args[1], ctx.Err())
		}
		return fmt.Errorf("goose %s failed: check PostgreSQL availability, access rights and migration SQL (tool output withheld to protect DATABASE_URL)", os.Args[1])
	}
	_, err = os.Stdout.Write(output)
	return err
}
