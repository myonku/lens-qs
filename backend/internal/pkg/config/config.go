// Package config 从环境变量读取配置（无配置文件，便于容器化部署）。
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config 是应用的全部可配置项。
type Config struct {
	// App
	Env  string // dev | prod
	Addr string // 监听地址，如 :8080

	// DB
	DBDriver  string // sqlite | postgres
	DBDSN     string // sqlite 文件路径，或 postgres 连接串
	DBMaxOpen int
	DBMaxIdle int

	// Auth
	SessionTTL     time.Duration // 会话绝对有效期
	SessionIdleTTL time.Duration // 空闲超时（超过则要求重新登录）
	MaxLoginFail   int           // 连续失败次数
	LockDuration   time.Duration // 达到失败上限后的锁定时长
	CookieName     string
	CookieSecure   bool
	CORSOrigins    []string
}

// Load 读取环境变量并返回配置。缺失项使用默认值。
//
// 会先尝试加载当前目录下的 .env（含上层的 backend/.env），
// 因此本地开发只需把 .env.example 复制成 .env 即可，无需手工设环境变量。
// **已存在的真实环境变量优先级更高，不会被 .env 覆盖**。
func Load() (*Config, error) {
	LoadDotEnv(".env", "backend/.env")

	c := &Config{
		Env:       env("APP_ENV", "dev"),
		Addr:      env("APP_ADDR", ":8080"),
		DBDriver:  strings.ToLower(env("DB_DRIVER", "sqlite")),
		DBDSN:     env("DB_DSN", "./data/lens.db"),
		DBMaxOpen: envInt("DB_MAX_OPEN", 10),
		DBMaxIdle: envInt("DB_MAX_IDLE", 5),

		SessionTTL:     envDuration("SESSION_TTL", 12*time.Hour),
		SessionIdleTTL: envDuration("SESSION_IDLE_TTL", 2*time.Hour),
		MaxLoginFail:   envInt("MAX_LOGIN_FAIL", 5),
		LockDuration:   envDuration("LOCK_DURATION", 10*time.Minute),
		CookieName:     env("COOKIE_NAME", "sid"),
		CookieSecure:   envBool("COOKIE_SECURE", false),
		CORSOrigins:    envList("CORS_ORIGINS", nil),
	}

	switch c.DBDriver {
	case "sqlite", "sqlite3":
		c.DBDriver = "sqlite"
	case "postgres", "postgresql", "pgx":
		c.DBDriver = "postgres"
	default:
		return nil, fmt.Errorf("不支持的 DB_DRIVER: %q（可选 sqlite | postgres）", c.DBDriver)
	}
	if c.DBMaxOpen < 1 || c.DBMaxIdle < 0 || c.DBMaxIdle > c.DBMaxOpen || c.SessionTTL <= 0 || c.SessionIdleTTL <= 0 || c.SessionIdleTTL > c.SessionTTL || c.MaxLoginFail < 1 || c.LockDuration <= 0 {
		return nil, fmt.Errorf("配置数值超出有效范围")
	}
	for _, origin := range c.CORSOrigins {
		if origin == "*" {
			return nil, fmt.Errorf("凭据来源不能使用通配符")
		}
	}
	return c, nil
}

// IsProd 报告当前是否为生产环境。
func (c *Config) IsProd() bool { return c.Env == "prod" || c.Env == "production" }

func env(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func envInt(k string, def int) int {
	if v, ok := os.LookupEnv(k); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

func envBool(k string, def bool) bool {
	if v, ok := os.LookupEnv(k); ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return def
}

func envDuration(k string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(k); ok {
		if d, err := time.ParseDuration(strings.TrimSpace(v)); err == nil {
			return d
		}
	}
	return def
}

func envList(k string, def []string) []string {
	v, ok := os.LookupEnv(k)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
