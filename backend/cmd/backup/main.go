package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"

	"github.com/myonku/lens-qs-api/internal/pkg/config"
	"github.com/myonku/lens-qs-api/internal/pkg/database"
	"github.com/myonku/lens-qs-api/internal/store"
)

func main() {
	path := flag.String("out", "", "new backup path (contains credentials; keep private)")
	receipt := flag.String("receipt", "", "new verification receipt path")
	flag.Parse()
	cfg, e := config.Load()
	if e != nil {
		log.Fatal(e)
	}
	if *path == "" || *receipt == "" {
		log.Fatal("-out and -receipt required")
	}
	var out store.BackupReceipt
	if cfg.DBDriver == "sqlite" {
		out, e = store.BackupSQLite(context.Background(), cfg.DBDSN, *path)
	} else {
		if _, err := os.Stat(*path); err != nil {
			log.Fatal("先生成 PostgreSQL 一致备份文件")
		}
		source, err := database.Open("postgres", cfg.DBDSN, 1, 1)
		if err != nil {
			log.Fatal("源库连接失败")
		}
		defer source.Close()
		restored, err := database.Open("postgres", os.Getenv("LENS_BACKUP_RESTORE_DSN"), 1, 1)
		if err != nil {
			log.Fatal("需要隔离恢复库 LENS_BACKUP_RESTORE_DSN")
		}
		defer restored.Close()
		original, err := store.Inspect(context.Background(), source, "postgres")
		if err != nil {
			log.Fatal("源库盘点失败")
		}
		copy, err := store.Inspect(context.Background(), restored, "postgres")
		if err != nil {
			log.Fatal("恢复库盘点失败")
		}
		if original.Target == copy.Target || original.DataDigest != copy.DataDigest {
			log.Fatal("隔离恢复库数据不一致或误指向源库")
		}
		out = store.BackupReceipt{SourceFingerprint: original.Fingerprint, SourceDigest: original.DataDigest, RestoredDigest: copy.DataDigest, BackupPath: *path}
	}

	if e != nil {
		log.Fatal(e)
	}
	raw, e := json.MarshalIndent(out, "", "  ")
	if e != nil {
		log.Fatal(e)
	}
	f, e := os.OpenFile(*receipt, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		log.Fatal(e)
	}
	defer f.Close()
	if _, e = f.Write(raw); e != nil {
		log.Fatal(e)
	}
	log.Print("consistent backup opened and verified; receipt written")
}
