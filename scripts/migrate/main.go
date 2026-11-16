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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/VladimirovArtem/template/internal/localenv"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	err := run(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: go run ./scripts/migrate {up|down|status}")
	}
	var action string
	switch os.Args[1] {
	case "up":
		action = "up"
	case "down":
		action = "down"
	case "status":
		action = "status"
	default:
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
		"-timeout", "1m", "-no-color", action)
	cmd.Env = append(os.Environ(), "GOOSE_DRIVER=postgres", "GOOSE_DBSTRING="+dsn)
	cmd.WaitDelay = time.Second
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("goose %s interrupted or timed out: %w", action, ctx.Err())
		}
		return fmt.Errorf("goose %s failed: check PostgreSQL availability, access rights and migration SQL (tool output withheld to protect DATABASE_URL)", action)
	}
	_, err = os.Stdout.Write(output)
	return err
}
