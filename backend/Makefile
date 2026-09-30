# lens-qs-api 常用命令
# 用法：make help

BINARY_DIR := bin
APP        := lens-qs-api
MIGRATE    := lens-qs-migrate
SEED       := lens-qs-seed
CREATEUSER := lens-qs-createuser

GOFLAGS ?=
DB_DRIVER ?= sqlite
DB_DSN    ?= ./data/lens.db
APP_ADDR  ?= :8080

export DB_DRIVER
export DB_DSN
export APP_ADDR

.PHONY: help
help: ## 显示可用命令
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: tidy
tidy: ## 整理依赖
	go mod tidy

.PHONY: build
build: ## 编译全部命令到 bin/
	go build -o $(BINARY_DIR)/$(APP) ./cmd/api
	go build -o $(BINARY_DIR)/$(MIGRATE) ./cmd/migrate
	go build -o $(BINARY_DIR)/$(SEED) ./cmd/seed
	go build -o $(BINARY_DIR)/$(CREATEUSER) ./cmd/createuser

.PHONY: run
run: ## 启动服务（前台）
	go run ./cmd/api

.PHONY: test
test: ## 运行全部测试
	go test -count=1 ./...

.PHONY: vet
vet: ## 静态检查
	go vet ./...

.PHONY: fmt
fmt: ## 格式化
	gofmt -s -w .

.PHONY: check
check: fmt vet test ## 格式化 + 静态检查 + 测试

.PHONY: migrate
migrate: ## 执行数据库迁移
	go run ./cmd/migrate

.PHONY: migrate-status
migrate-status: ## 查看迁移状态
	go run ./cmd/migrate -status

.PHONY: seed
seed: ## 写入字典与参考部件号
	go run ./cmd/seed

.PHONY: createuser
createuser: ## 创建用户（示例：make createuser U=admin P=xxx ADMIN=1）
	go run ./cmd/createuser -username $(U) -password $(P) -admin=$(ADMIN) -name "$(N)"

.PHONY: docker-up
docker-up: ## 用 docker compose 启动（PostgreSQL + API）
	docker compose up -d --build

.PHONY: docker-down
docker-down: ## 停止 docker compose
	docker compose down

.PHONY: clean
clean: ## 清理构建产物与本地数据库
	rm -rf $(BINARY_DIR)
	rm -f data/*.db data/*.db-shm data/*.db-wal
