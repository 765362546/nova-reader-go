#!/usr/bin/env bash
# 一键构建：编译前端并内嵌到 Go 二进制，避免遗漏前端步骤。
#
#   ./build.sh                  # 构建前端 + 后端
#   SKIP_FRONTEND=1 ./build.sh  # 仅重新编译后端（复用已有 web/dist）
set -euo pipefail
cd "$(dirname "$0")"

if [ "${SKIP_FRONTEND:-0}" != "1" ]; then
  if ! command -v npm >/dev/null 2>&1; then
    echo "错误: 未找到 npm，请先安装 Node.js (14~16，或 17+ 并允许 legacy OpenSSL)" >&2
    exit 1
  fi

  # Node.js 17+ 需要 legacy provider 才能运行 Vue CLI 4 的 webpack 4。
  if command -v node >/dev/null 2>&1; then
    node_major="$(node -p 'process.versions.node.split(".")[0]')"
    if [ "$node_major" -ge 17 ]; then
      export NODE_OPTIONS="${NODE_OPTIONS:-} --openssl-legacy-provider"
    fi
  fi

  echo "==> 构建前端 (web/dist)"
  if [ -f web/package-lock.json ]; then
    (cd web && npm ci)
  else
    (cd web && npm install)
  fi
  (cd web && npm run build)
  if [ ! -f web/dist/index.html ]; then
    echo "错误: 前端构建失败，未生成 web/dist/index.html" >&2
    exit 1
  fi
  # 构建会清空 dist；保留占位文件，保证未构建前端时 go:embed 仍可编译。
  touch web/dist/.gitkeep
else
  echo "==> 跳过前端构建 (SKIP_FRONTEND=1)"
  if [ ! -f web/dist/index.html ]; then
    echo "警告: web/dist/index.html 不存在，二进制将不包含前端资源" >&2
  fi
fi

echo "==> 编译 Go 二进制"
# 预生成 .gz 静态资源，服务端在 Accept-Encoding 含 gzip 时直接返回，省去运行时压缩。
if [ -f web/dist/index.html ] && command -v gzip >/dev/null 2>&1; then
  echo "==> 预压缩静态资源 (gzip -9)"
  find web/dist -type f \( -name '*.js' -o -name '*.css' -o -name '*.html' \
      -o -name '*.svg' -o -name '*.json' -o -name '*.map' \) -print0 |
    while IFS= read -r -d '' f; do
      [ -f "$f.gz" ] || gzip -9 -k -f "$f"
    done
fi
go build -o novel-server ./cmd/server
echo "完成: ./novel-server"
