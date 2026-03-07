package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// MigrateUp applies all pending schema migrations using the direct Neon URL.
func MigrateUp(ctx context.Context, directURL string) error {
	directURL = strings.TrimSpace(directURL)
	if directURL == "" {
		return errors.New("DATABASE_URL_DIRECT is required")
	}
	if strings.Contains(directURL, "-pooler") {
		return errors.New("DATABASE_URL_DIRECT must be a direct Neon URL, not a pooler URL")
	}
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("configure goose dialect: %w", err)
	}

	dir := filepath.Join("internal", "db", "migrations")
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("migrations directory not found at %q: %w", dir, err)
	}

	db, err := sql.Open("pgx", directURL)
	if err != nil {
		return fmt.Errorf("open direct database: %w", err)
	}
	defer db.Close()

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return fmt.Errorf("ping direct database: %w", err)
	}

	if err := goose.UpContext(ctx, db, dir); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
