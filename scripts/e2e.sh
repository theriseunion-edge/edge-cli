#!/usr/bin/env bash
# 用本机 docker registry 验证上传、下载、形态保持、内容指纹与冲突处理。
set -euo pipefail

cd "$(dirname "$0")/.."
BIN=./bin/ec
CONTAINER=ec-e2e-registry
PORT=${E2E_REGISTRY_PORT:-$(shuf -i 20000-39999 -n1)}
REGISTRY="127.0.0.1:${PORT}"
WORK=$(mktemp -d)
LOG=$(mktemp)

cleanup() {
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  rm -rf "$WORK"
  rm -f "$LOG"
}
trap cleanup EXIT

log() { echo "== $*"; }
fail() { echo "E2E 失败: $*" >&2; exit 1; }

start_registry() {
  if docker image inspect registry:2 >/dev/null 2>&1; then
    :
  else
    docker pull registry:2 >/dev/null
  fi
  docker run -d --rm --name "$CONTAINER" -p "127.0.0.1:${PORT}:5000" registry:2 >/dev/null
  for _ in $(seq 1 50); do
    if curl -fsS "http://${REGISTRY}/v2/" >/dev/null 2>&1; then return 0; fi
    sleep 0.2
  done
  fail "本地 registry 未就绪"
}

make_source() {
  mkdir -p "$WORK/source/bin" "$WORK/source/empty"
  printf 'edge offline package %s\n' "$1" > "$WORK/source/README.md"
  printf '#!/bin/sh\necho install\n' > "$WORK/source/bin/install.sh"
  chmod 755 "$WORK/source/bin/install.sh"
  head -c 3000000 /dev/urandom > "$WORK/source/images.bin"
  chmod 644 "$WORK/source/images.bin"
}

json_field() { python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$1" "$2"; }

start_registry
make_source dir

log "目录形态：上传"
"$BIN" package push -i "$WORK/source" -o "${REGISTRY}/edge/offline:v-3.0.0" --format json > "$WORK/push-dir.json"
FINGERPRINT=$(json_field "$WORK/push-dir.json" fingerprint)

log "目录形态：下载并校验"
"$BIN" package pull -i "${REGISTRY}/edge/offline:v-3.0.0" -o "$WORK/restored-dir" --format json > "$WORK/pull-dir.json"
PULLED=$(json_field "$WORK/pull-dir.json" fingerprint)
[ "$PULLED" = "$FINGERPRINT" ] || fail "目录指纹不一致: $FINGERPRINT != $PULLED"
diff -r "$WORK/source" "$WORK/restored-dir" >/dev/null || fail "目录内容不一致"

log "目录形态：重复上传复用"
"$BIN" package push -i "$WORK/source" -o "${REGISTRY}/edge/offline:v-3.0.0" >/dev/null 2>&1 || fail "同内容重传失败"

log "目录形态：同 tag 不同内容拒绝"
printf 'changed\n' >> "$WORK/source/README.md"
if "$BIN" package push -i "$WORK/source" -o "${REGISTRY}/edge/offline:v-3.0.0" >/dev/null 2>&1; then
  fail "同 tag 不同内容应当失败"
fi

log "压缩包形态：上传下载并比对字节"
make_source tar
tar -czf "$WORK/source.tar.gz" -C "$WORK/source" .
"$BIN" package push -i "$WORK/source.tar.gz" -o "${REGISTRY}/edge/offline:tar-v-3.0.0" --format json > "$WORK/push-tar.json"
"$BIN" package pull -i "${REGISTRY}/edge/offline:tar-v-3.0.0" -o "$WORK/restored.tar.gz" >/dev/null
cmp "$WORK/source.tar.gz" "$WORK/restored.tar.gz" || fail "压缩包字节不一致"

log "输出已存在时拒绝"
if "$BIN" package pull -i "${REGISTRY}/edge/offline:v-3.0.0" -o "$WORK/restored-dir" >/dev/null 2>&1; then
  fail "输出已存在应当失败"
fi

echo "E2E 通过"
