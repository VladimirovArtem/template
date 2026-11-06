package config

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

func parseDatabaseURL(rawURL string) (*pgxpool.Config, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u == nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") ||
		u.Hostname() == "" || !validHost(u.Hostname()) || u.User == nil || u.User.Username() == "" ||
		strings.Trim(u.Path, "/") == "" || u.Fragment != "" ||
		(u.Port() != "" && !validPort(u.Port())) || strings.HasSuffix(u.Host, ":") {
		return nil, fmt.Errorf("DATABASE_URL: required postgres:// or postgresql:// URL with host, user and database; port must be in 1..65535")
	}
	pool, err := pgxpool.ParseConfig(rawURL)
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL: invalid PostgreSQL connection options")
	}
	return pool, nil
}
