package database

import (
	"path/filepath"
	"testing"
)

func TestSQLiteURIKeepsConnectionPragmas(t *testing.T) {
	for _, suffix := range []string{"", "?mode=rwc"} {
		db, e := Open("sqlite", filepath.Join(t.TempDir(), "settings.db")+suffix, 4, 1)
		if e != nil {
			t.Fatal(e)
		}
		for pragma, want := range map[string]int{"foreign_keys": 1, "temp_store": 2, "busy_timeout": 5000} {
			var got int
			if e = db.Get(&got, "PRAGMA "+pragma); e != nil || got != want {
				t.Fatal(pragma, got, want, e)
			}
		}
		if db.Stats().MaxOpenConnections != 1 {
			t.Fatal("sqlite write pool not bounded")
		}
		db.Close()
	}
}
