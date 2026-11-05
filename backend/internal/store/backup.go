package store

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/myonku/lens-qs-api/internal/pkg/database"
)

type BackupReceipt struct {
	SourceFingerprint string `json:"source_fingerprint"`
	SourceDigest      string `json:"source_digest"`
	RestoredDigest    string `json:"restored_digest"`
	BackupPath        string `json:"backup_path"`
}

func ReadOnlySQLiteDSN(dsn string) string {
	parts := strings.SplitN(dsn, "?", 2)
	name := strings.TrimPrefix(parts[0], "file:")
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", "query_only(1)")
	return "file:" + name + "?" + q.Encode()
}

// BackupSQLite creates a consistent standalone database through SQLite itself,
// including WAL contents. The source is opened read-only and never reconfigured.
func BackupSQLite(ctx context.Context, dsn, destination string) (BackupReceipt, error) {
	var out BackupReceipt
	if _, e := os.Stat(destination); !os.IsNotExist(e) {
		return out, fmt.Errorf("backup destination already exists or cannot be inspected")
	}
	source, e := database.Open("sqlite", ReadOnlySQLiteDSN(dsn), 1, 1)
	if e != nil {
		return out, e
	}
	defer source.Close()
	inv, e := Inspect(ctx, source, "sqlite")
	if e != nil {
		return out, e
	}
	// VACUUM INTO writes only the separate destination, requiring query_only off
	// while the main database remains mode=ro at the SQLite VFS boundary.
	vacuumDSN := strings.Replace(ReadOnlySQLiteDSN(dsn), "query_only%281%29", "query_only%280%29", 1)
	conn, e := sqlx.Open("sqlite", vacuumDSN)
	if e != nil {
		return out, e
	}
	defer conn.Close()
	if _, e = conn.ExecContext(ctx, `VACUUM main INTO ?`, destination); e != nil {
		return out, e
	}
	restore, e := database.Open("sqlite", ReadOnlySQLiteDSN(destination), 1, 1)
	if e != nil {
		return out, e
	}
	defer restore.Close()
	restored, e := Inspect(ctx, restore, "sqlite")
	if e != nil {
		return out, e
	}
	if inv.DataDigest != restored.DataDigest {
		return out, fmt.Errorf("restored backup differs from source")
	}
	after, e := Inspect(ctx, source, "sqlite")
	if e != nil {
		return out, e
	}
	if inv.Fingerprint != after.Fingerprint {
		return out, fmt.Errorf("source changed during backup; stop writes and repeat")
	}
	return BackupReceipt{inv.Fingerprint, inv.DataDigest, restored.DataDigest, destination}, nil
}
