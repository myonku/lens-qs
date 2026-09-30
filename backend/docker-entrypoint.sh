#!/bin/sh
# 容器入口：先执行迁移与（可选的）初始化数据，再启动 API。
set -e

echo "[entrypoint] 等待数据库就绪…"
i=0
until /app/lens-qs-migrate -status >/dev/null 2>&1; do
  i=$((i + 1))
  if [ "$i" -ge 30 ]; then
    echo "[entrypoint] 数据库在 30 次尝试后仍不可用，退出"
    exit 1
  fi
  sleep 2
done

echo "[entrypoint] 执行数据库迁移…"
/app/lens-qs-migrate

if [ "${SEED_ON_START:-true}" = "true" ]; then
  echo "[entrypoint] 初始化字典与参考部件号…"
  /app/lens-qs-seed -skip-migrate || echo "[entrypoint] 初始化数据跳过（可以稍后手动执行）"
fi

if [ -n "${BOOTSTRAP_ADMIN_USER}" ] && [ -n "${BOOTSTRAP_ADMIN_PASS}" ]; then
  echo "[entrypoint] 检查初始管理员 ${BOOTSTRAP_ADMIN_USER}…"
  /app/lens-qs-createuser -skip-migrate \
    -username "${BOOTSTRAP_ADMIN_USER}" -password "${BOOTSTRAP_ADMIN_PASS}" \
    -admin -name "${BOOTSTRAP_ADMIN_NAME:-管理员}" 2>/dev/null \
    || /app/lens-qs-createuser -skip-migrate -update \
       -username "${BOOTSTRAP_ADMIN_USER}" -password "${BOOTSTRAP_ADMIN_PASS}" \
       -admin -name "${BOOTSTRAP_ADMIN_NAME:-管理员}"
fi

echo "[entrypoint] 启动服务：$*"
exec "$@"
