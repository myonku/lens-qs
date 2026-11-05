package store

import (
	"context"
	"fmt"
	"github.com/jmoiron/sqlx"
)

// Upgrade is an explicit, fingerprint-bound, non-destructive v7 to v8 upgrade.
// No API startup path invokes it. All data and existing checksums are retained.
func Upgrade(ctx context.Context, db *sqlx.DB, driver, expected string) error {
	inv, e := Inspect(ctx, db, driver)
	if e != nil {
		return e
	}
	if expected == "" || inv.Fingerprint != expected {
		return fmt.Errorf("target fingerprint mismatch")
	}
	if inv.Version == 8 {
		return CheckReady(ctx, db, driver)
	}
	if inv.Version != 7 {
		return fmt.Errorf("upgrade requires known v7")
	}
	_, e = migrate(ctx, db, driver, 8, true, inv.DataDigest)
	return e
}
