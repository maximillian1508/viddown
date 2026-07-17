package main

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func sqliteDSN(dbPath string) string {
	return fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", dbPath)
}

// runMigrations applies pending SQL migrations using a dedicated connection.
// migrate.Close() closes that connection; the app uses a separate pool afterward.
func runMigrations(dbPath string) error {
	migrateDB, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		return fmt.Errorf("migrate open db: %w", err)
	}

	driver, err := sqlite3.WithInstance(migrateDB, &sqlite3.Config{})
	if err != nil {
		_ = migrateDB.Close()
		return fmt.Errorf("migrate sqlite driver: %w", err)
	}

	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		_ = migrateDB.Close()
		return fmt.Errorf("migrate source: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, filepath.Base(dbPath), driver)
	if err != nil {
		_ = migrateDB.Close()
		return fmt.Errorf("migrate instance: %w", err)
	}

	upErr := m.Up()
	_, _ = m.Close()
	if upErr != nil && !errors.Is(upErr, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", upErr)
	}
	return nil
}
