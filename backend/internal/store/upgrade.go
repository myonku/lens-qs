package store

import (
	"context"
	"fmt"
	"github.com/jmoiron/sqlx"
)

// Upgrade is an explicit, fingerprint-bound, non-destructive upgrade from schema 7, 8, or 9.
// No API startup path invokes it. All data and existing checksums are retained.
func Upgrade(ctx context.Context, db *sqlx.DB, driver, expected string) error {
	inv, e := Inspect(ctx, db, driver)
	if e != nil {
		return e
	}
	if expected == "" || inv.Fingerprint != expected {
		return fmt.Errorf("target fingerprint mismatch")
	}
	if inv.Version == LatestSchemaVersion {
		return CheckReady(ctx, db, driver)
	}
	if inv.Version != 7 && inv.Version != 8 && inv.Version != 9 {
		return fmt.Errorf("upgrade requires known v7, v8, or v9")
	}
	_, e = migrate(ctx, db, driver, LatestSchemaVersion, true, inv.DataDigest)
	return e
}
