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

	"github.com/gin-gonic/gin"

	"github.com/myonku/lens-qs-api/internal/auth"
	"github.com/myonku/lens-qs-api/internal/dictionary"
	"github.com/myonku/lens-qs-api/internal/idempotency"
	"github.com/myonku/lens-qs-api/internal/inspection"
	"github.com/myonku/lens-qs-api/internal/ledger"
	"github.com/myonku/lens-qs-api/internal/pkg/config"
	"github.com/myonku/lens-qs-api/internal/pkg/database"
	"github.com/myonku/lens-qs-api/internal/sample"
	"github.com/myonku/lens-qs-api/internal/server"
	"github.com/myonku/lens-qs-api/internal/stats"
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
	if cfg.AutoMigrate {
		applied, err := store.Migrate(ctx, db, cfg.DBDriver)
		if err != nil {
			log.Fatalf("[fatal] 自动迁移失败: %v", err)
		}
		if len(applied) > 0 {
			log.Printf("[info] 已应用迁移: %v", applied)
		}
	}

	// 依赖装配
	userStore := users.NewStore(db, cfg.DBDriver)
	authSvc := auth.NewService(db, userStore, auth.Config{
		SessionTTL:     cfg.SessionTTL,
		SessionIdleTTL: cfg.SessionIdleTTL,
		MaxLoginFail:   cfg.MaxLoginFail,
		LockDuration:   cfg.LockDuration,
	}, cfg.DBDriver)

	dictStore := dictionary.NewStore(db, cfg.DBDriver)
	inspStore := inspection.NewStore(db, cfg.DBDriver)
	inspSvc := inspection.NewService(inspStore)
	sampleSvc := sample.NewService(db, dictStore, cfg.DBDriver)
	statsSvc := stats.NewService(db, cfg.DBDriver)

	// 幂等：IDEMPOTENCY_TTL <= 0 时关闭
	var idemStore *idempotency.Store
	if cfg.IdempotencyTTL > 0 {
		idemStore = idempotency.NewStore(db, cfg.DBDriver, cfg.IdempotencyTTL)
		log.Printf("[info] 幂等已启用（TTL=%s；键来源：X-Idempotency-Key 或请求体 client_request_id）", cfg.IdempotencyTTL)
	}

	// 首次启动时按环境变量创建管理员（数据库为空时才创建）
	if err := bootstrapAdmin(ctx, userStore, cfg); err != nil {
		log.Printf("[warn] 初始化管理员失败: %v", err)
	}

	engine := server.Build(server.Deps{
		Cfg:         cfg,
		Ledger:      ledger.New(db),
		Auth:        authSvc,
		UserStore:   userStore,
		Dict:        dictStore,
		Sample:      sampleSvc,
		Inspection:  inspSvc,
		Stats:       statsSvc,
		Idempotency: idemStore,
	})

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           engine,
		ReadHeaderTimeout: 10 * time.Second,
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

func bootstrapAdmin(ctx context.Context, userStore *users.Store, cfg *config.Config) error {
	if cfg.BootstrapAdminUser == "" || cfg.BootstrapAdminPass == "" {
		return nil
	}
	n, err := userStore.Count(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	u, err := userStore.Create(ctx, users.CreateInput{
		Username:      cfg.BootstrapAdminUser,
		Password:      cfg.BootstrapAdminPass,
		DisplayName:   cfg.BootstrapAdminUser,
		IsAdmin:       true,
		MustChangePwd: false,
	})
	if err != nil {
		return err
	}
	log.Printf("[info] 已创建初始管理员: %s (id=%s)", u.Username, u.ID)
	return nil
}

// 保证 gin 引用（server 包已使用；此处保留便于将来在此处设置模式）。
var _ = gin.Version
