package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"

	"github.com/myonku/lens-qs-api/internal/pkg/config"
	"github.com/myonku/lens-qs-api/internal/pkg/database"
	"github.com/myonku/lens-qs-api/internal/store"
)

func main() {
	confirm := flag.Bool("confirm-test-data", false, "clear inspection test data after backup and stopped writes")
	target := flag.String("target-fingerprint", "", "must match preview")
	flag.Parse()
	cfg, e := config.Load()
	if e != nil {
		log.Fatal(e)
	}
	dsn := cfg.DBDSN
	if !*confirm && cfg.DBDriver == "sqlite" {
		dsn = store.ReadOnlySQLiteDSN(dsn)
	}
	db, e := database.Open(cfg.DBDriver, dsn, 1, 1)
	if e != nil {
		log.Fatal("无法打开维护目标")
	}
	defer db.Close()
	ctx := context.Background()
	inv, e := store.Inspect(ctx, db, cfg.DBDriver)
	if e != nil {
		log.Fatal(e)
	}
	if !*confirm {
		b, _ := json.MarshalIndent(inv, "", "  ")
		fmt.Println(string(b))
		return
	}
	if *target == "" || *target != inv.Fingerprint || inv.Version != store.LatestSchemaVersion {
		log.Fatal("目标指纹或结构不符")
	}
	if e = store.ResetInspection(ctx, db); e != nil {
		log.Fatal(e)
	}
	fmt.Println("inspection data, sessions, domain idempotency and automatic cache cleared; accounts/configuration/manual confirmations retained")
}
