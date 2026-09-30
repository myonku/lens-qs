// Command migrate 执行数据库迁移。
//
// 用法：
//
//	go run ./cmd/migrate            # 应用全部未执行的迁移
//	go run ./cmd/migrate -status    # 仅查看已应用版本
package main

import (
	"context"
	"flag"
	"log"

	"github.com/myonku/lens-qs-api/internal/pkg/config"
	"github.com/myonku/lens-qs-api/internal/pkg/database"
	"github.com/myonku/lens-qs-api/internal/store"
)

func main() {
	statusOnly := flag.Bool("status", false, "仅打印已应用的迁移版本")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("[fatal] 配置错误: %v", err)
	}
	db, err := database.Open(cfg.DBDriver, cfg.DBDSN, cfg.DBMaxOpen, cfg.DBMaxIdle)
	if err != nil {
		log.Fatalf("[fatal] 数据库不可用: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if *statusOnly {
		applied, err := store.AppliedMigrations(ctx, db)
		if err != nil {
			log.Fatalf("[fatal] 读取迁移状态失败: %v", err)
		}
		if len(applied) == 0 {
			log.Println("[info] 尚无已应用的迁移")
			return
		}
		for _, m := range applied {
			log.Printf("[info] 已应用: v%d %s (%s)", m.Version, m.Name, m.AppliedAt)
		}
		return
	}

	applied, err := store.Migrate(ctx, db, cfg.DBDriver)
	if err != nil {
		log.Fatalf("[fatal] 迁移失败: %v", err)
	}
	if len(applied) == 0 {
		log.Println("[info] 数据库已是最新，无需迁移")
		return
	}
	log.Printf("[info] 已应用 %d 个迁移: %v", len(applied), applied)
}
