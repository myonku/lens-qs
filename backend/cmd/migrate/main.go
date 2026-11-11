// Command migrate is an explicit offline maintenance boundary. Status/preview
// never create migration tables. Rebuild requires a verified stopped-write backup.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/myonku/lens-qs-api/internal/pkg/config"
	"github.com/myonku/lens-qs-api/internal/pkg/database"
	"github.com/myonku/lens-qs-api/internal/store"
)

func main() {
	status := flag.Bool("status", false, "read-only inventory")
	initialize := flag.Bool("initialize", false, "initialize an empty database")
	rebuild := flag.Bool("rebuild-test-data", false, "explicitly rebuild known v6 inspection test data")
	upgrade := flag.Bool("upgrade", false, "explicit non-destructive upgrade from schema 7, 8 or 9")
	target := flag.String("target-fingerprint", "", "fingerprint from status")
	backup := flag.String("verified-backup", "", "path to restored-and-verified backup receipt")
	flag.Parse()
	cfg, e := config.Load()
	if e != nil {
		log.Fatal(e)
	}
	// Read-only SQLite opens must not create a missing file or change journal mode.
	var dbDSN = cfg.DBDSN
	if *status && cfg.DBDriver == "sqlite" {
		dbDSN = store.ReadOnlySQLiteDSN(dbDSN)
	}
	db, e := database.Open(cfg.DBDriver, dbDSN, 1, 1)
	if e != nil {
		log.Fatal("无法打开维护目标")
	}
	defer db.Close()
	ctx := context.Background()
	if *initialize {
		e = store.Initialize(ctx, db, cfg.DBDriver)
	} else if *rebuild || *upgrade {
		raw, err := os.ReadFile(*backup)
		if err != nil {
			log.Fatal("需要已验证备份回执")
		}
		var receipt store.BackupReceipt
		if json.Unmarshal(raw, &receipt) != nil || receipt.SourceFingerprint != *target || receipt.SourceDigest == "" || receipt.RestoredDigest != receipt.SourceDigest {
			log.Fatal("备份恢复验证回执与目标不符")
		}
		inv, err := store.Inspect(ctx, db, cfg.DBDriver)
		if err != nil || inv.DataDigest != receipt.SourceDigest {
			log.Fatal("目标数据与备份回执不符")
		}
		if *upgrade {
			e = store.Upgrade(ctx, db, cfg.DBDriver, *target)
		} else {
			e = store.Rebuild(ctx, db, cfg.DBDriver, *target)
		}
	} else {
		inv, err := store.Inspect(ctx, db, cfg.DBDriver)
		if err != nil {
			log.Fatal("盘点失败: ", err)
		}
		b, _ := json.MarshalIndent(inv, "", "  ")
		fmt.Println(string(b))
		return
	}
	if e != nil {
		log.Fatal(e)
	}
	fmt.Println("maintenance complete")
}
