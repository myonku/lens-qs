// Command api 启动 HTTP 服务。
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/myonku/lens-qs-api/internal/auth"
	"github.com/myonku/lens-qs-api/internal/dictionary"
	"github.com/myonku/lens-qs-api/internal/pkg/config"
	"github.com/myonku/lens-qs-api/internal/pkg/database"
	"github.com/myonku/lens-qs-api/internal/server"
	"github.com/myonku/lens-qs-api/internal/store"
	"github.com/myonku/lens-qs-api/internal/users"
)

func main() {
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
		log.Fatal("数据库结构不兼容，请先执行显式维护命令: ", err)
	}

	// 依赖装配
	userStore := users.NewStore(db, cfg.DBDriver)
	if _, err := userStore.BootstrapAdmin(ctx, users.CreateInput{
		Username: cfg.BootstrapAdminUser, Password: cfg.BootstrapAdminPass,
		AccountType: cfg.BootstrapAdminType, DisplayName: cfg.BootstrapAdminName,
		EmployeeNo: optional(cfg.BootstrapAdminEmp), Department: optional(cfg.BootstrapAdminDepartment),
	}); err != nil {
		log.Fatalf("[fatal] 管理员引导失败: %v", err)
	}
	authSvc := auth.NewService(db, userStore, auth.Config{
		SessionTTL:     cfg.SessionTTL,
		SessionIdleTTL: cfg.SessionIdleTTL,
		MaxLoginFail:   cfg.MaxLoginFail,
		LockDuration:   cfg.LockDuration,
	}, cfg.DBDriver)

	dictStore := dictionary.NewStore(db, cfg.DBDriver)

	engine := server.Build(server.Deps{
		Cfg:       cfg,
		Auth:      authSvc,
		UserStore: userStore,
		Dict:      dictStore,
	})

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           engine,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Printf("[info] lens-qs-api 启动于 %s（env=%s, db=%s）", cfg.Addr, cfg.Env, cfg.DBDriver)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("[fatal] 服务启动失败: %v", err)
		}
	}()

	// 优雅退出
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("[info] 收到退出信号，正在关闭…")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("[warn] 关闭超时: %v", err)
	}
	log.Println("[info] 已退出")
}

func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
