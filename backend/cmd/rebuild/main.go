// Command rebuild inventories or stages legacy records, and imports explicitly reviewed mappings.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/myonku/lens-qs-api/internal/ledger"
	"github.com/myonku/lens-qs-api/internal/pkg/config"
	"github.com/myonku/lens-qs-api/internal/pkg/database"
	"log"
	"os"
)

func main() {
	stage := flag.Bool("stage", false, "保留旧记录原始证据到待核对区，不归组不发布")
	reviewed := flag.String("import-reviewed", "", "人工核对清单 JSON 路径；只创建草稿")
	actor := flag.String("actor", "", "执行人员的现有用户 ID（写入时必填）")
	key := flag.String("key", "", "本次操作幂等键（写入时必填）")
	flag.Parse()
	cfg, e := config.Load()
	if e != nil {
		log.Fatal(e)
	}
	db, e := database.Open(cfg.DBDriver, cfg.DBDSN, 1, 1)
	if e != nil {
		log.Fatal(e)
	}
	defer db.Close()
	svc := ledger.New(db)
	ctx := context.Background()
	if !*stage && *reviewed == "" {
		v, e := svc.Inventory(ctx)
		if e != nil {
			log.Fatal(e)
		}
		b, _ := json.MarshalIndent(v, "", "  ")
		fmt.Println(string(b))
		return
	}
	if *stage && *reviewed != "" {
		log.Fatal("stage 和 import-reviewed 不能同时指定")
	}
	if *actor == "" || *key == "" {
		log.Fatal("写入必须提供 actor 和 key")
	}
	if cfg.BusinessWriteDomain != "v2" {
		log.Fatal("导入要求 BUSINESS_WRITE_DOMAIN=v2，旧业务域必须只读")
	}
	var b []byte
	if *stage {
		b, _, _, e = svc.Stage(ctx, *actor, *key)
	} else {
		var raw []byte
		raw, e = os.ReadFile(*reviewed)
		if e == nil {
			b, _, _, e = svc.ImportReviewed(ctx, *actor, *key, raw)
		}
	}
	if e != nil {
		log.Fatal(e)
	}
	fmt.Println(string(b))
}
