// Command createuser 创建或更新一个用户（用于首次部署时建立管理员）。
//
// 用法：
//
//	go run ./cmd/createuser -username admin -password 'xxxx' -admin
//	go run ./cmd/createuser -username zhangsai -password 'xxxx' -name 张赛赛 -emp 1001
package main

import (
	"context"
	"flag"
	"log"

	"github.com/myonku/lens-qs-api/internal/pkg/config"
	"github.com/myonku/lens-qs-api/internal/pkg/database"
	"github.com/myonku/lens-qs-api/internal/pkg/httpx"
	"github.com/myonku/lens-qs-api/internal/store"
	"github.com/myonku/lens-qs-api/internal/users"
)

func main() {
	username := flag.String("username", "", "登录名（必填）")
	password := flag.String("password", "", "密码（必填）")
	name := flag.String("name", "", "显示名（默认同登录名）")
	empNo := flag.String("emp", "", "工号")
	department := flag.String("department", "", "所属部门文字")
	accountType := flag.String("account-type", "", "NORMAL 或 SAMPLING；创建默认 NORMAL，更新省略则保持")
	isAdmin := flag.Bool("admin", false, "是否管理员")
	update := flag.Bool("update", false, "已存在时更新（改密码 / 改管理员标记）")
	flag.Parse()

	if *username == "" || *password == "" {
		log.Fatal("[fatal] 必须提供 -username 与 -password")
	}

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
	if err := store.CheckReady(ctx, db, cfg.DBDriver); err != nil {
		log.Fatal(err)
	}

	st := users.NewStore(db, cfg.DBDriver)
	existing, err := st.GetByUsername(ctx, *username)
	if err != nil {
		log.Fatalf("[fatal] 查询用户失败: %v", err)
	}

	if existing != nil {
		if !*update {
			log.Fatalf("[fatal] 用户已存在: %s（如需更新请加 -update）", *username)
		}
		u, err := st.Update(ctx, existing.ID, users.UpdateInput{AccountType: opt(*accountType),
			DisplayName: opt(*name),
			EmployeeNo:  httpx.Optional[string]{Set: *empNo != "", Value: opt(*empNo)},
			Department:  httpx.Optional[string]{Set: *department != "", Value: opt(*department)},
			IsAdmin:     &*isAdmin,
			Password:    password,
		})
		if err != nil {
			log.Fatalf("[fatal] 更新用户失败: %v", err)
		}
		log.Printf("[info] 已更新用户: %s (id=%s, admin=%v)", u.Username, u.ID, u.IsAdmin)
		return
	}

	u, err := st.Create(ctx, users.CreateInput{AccountType: *accountType,
		Username:      *username,
		Password:      *password,
		DisplayName:   *name,
		EmployeeNo:    opt(*empNo),
		Department:    opt(*department),
		IsAdmin:       *isAdmin,
		MustChangePwd: false,
	})
	if err != nil {
		log.Fatalf("[fatal] 创建用户失败: %v", err)
	}
	log.Printf("[info] 已创建用户: %s (id=%s, admin=%v)", u.Username, u.ID, u.IsAdmin)
}

func opt(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
