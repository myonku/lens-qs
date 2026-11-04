// Command reset-testdata clears inspection test data without resetting accounts or dictionaries.
package main

import (
	"context"
	"flag"
	"github.com/myonku/lens-qs-api/internal/ledger"
	"github.com/myonku/lens-qs-api/internal/pkg/config"
	"github.com/myonku/lens-qs-api/internal/pkg/database"
	"log"
)

func main() {
	confirm := flag.Bool("confirm-test-data", false, "确认目标库业务资料均为可清除测试数据（保留账户与字典）")
	flag.Parse()
	if !*confirm {
		log.Fatal("必须显式传入 -confirm-test-data；请先备份并停写")
	}
	cfg, e := config.Load()
	if e != nil {
		log.Fatal(e)
	}
	db, e := database.Open(cfg.DBDriver, cfg.DBDSN, 1, 1)
	if e != nil {
		log.Fatal(e)
	}
	defer db.Close()
	if e = ledger.New(db).ResetTestData(context.Background()); e != nil {
		log.Fatal(e)
	}
	log.Print("v2 测试业务、会话、幂等与解析缓存已清除；账户密码及字典保留")
}
