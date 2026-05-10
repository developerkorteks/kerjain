package whatsapp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"

	_ "modernc.org/sqlite"
)

const dbPath = "./data/whatsapp.db"

func initStore(ctx context.Context) (*sqlstore.Container, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	logger := waLog.Stdout("DB", "WARN", true)
	dsn := "file:" + dbPath + "?_pragma=foreign_keys(on)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)"
	container, err := sqlstore.New(ctx, "sqlite", dsn, logger)
	if err != nil {
		return nil, fmt.Errorf("open sqlite store: %w", err)
	}
	return container, nil
}

func wipeStore() error {
	if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove db: %w", err)
	}
	return nil
}
