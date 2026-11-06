#!/usr/bin/env bash
set -euo pipefail

BIN_DIR="${SWITCHYARD_BIN_DIR:-/opt/switchyard/bin}"
HEALTH_URL="${SWITCHYARD_HEALTH_URL:-http://127.0.0.1:8088/healthz}"
HEALTH_RETRIES="${SWITCHYARD_HEALTH_RETRIES:-20}"
HEALTH_INTERVAL="${SWITCHYARD_HEALTH_INTERVAL:-1}"
DRY_RUN="${SWITCHYARD_DRY_RUN:-0}"
EXPECTED_VERSION="${1:-}"
SERVICE=switchyard
NEW_BIN="${BIN_DIR}/switchyard.new"
CURRENT_BIN="${BIN_DIR}/switchyard"
BACKUP_BIN=""
HEALTH_VERSION=""
HEALTH_UPTIME=""

run_service() {
  if [[ "$DRY_RUN" == 1 ]]; then printf '[dry-run] systemctl %s %s\n' "$1" "$SERVICE"; return 0; fi
  systemctl "$1" "$SERVICE"
}

probe_health() {
  local body http_code curl_exit parsed
  body="$(mktemp)"
  if http_code="$(curl -sS --max-time 5 -o "$body" -w '%{http_code}' "$HEALTH_URL")"; then curl_exit=0; else curl_exit=$?; fi
  if [[ "$curl_exit" -ne 0 || "$http_code" != 200 ]]; then rm -f "$body"; return 1; fi
  if ! parsed="$(python3 - "$body" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f: data = json.load(f)
if data.get("status") != "ok" or data.get("db") != "ok": raise SystemExit(1)
print(data.get("version", "")); print(data.get("uptime_seconds", ""))
PY
)"; then rm -f "$body"; return 1; fi
  rm -f "$body"
  HEALTH_VERSION="$(printf '%s\n' "$parsed" | sed -n '1p')"
  HEALTH_UPTIME="$(printf '%s\n' "$parsed" | sed -n '2p')"
  if [[ -n "$EXPECTED_VERSION" && "$HEALTH_VERSION" != "$EXPECTED_VERSION" ]]; then return 1; fi
  printf 'health ok version=%s uptime_seconds=%s\n' "$HEALTH_VERSION" "$HEALTH_UPTIME"
}

wait_for_health() {
  local attempt
  # 默认最多 20 次, 每次 curl 最多 5 秒且间隔 1 秒, 上界约 120 秒(20*(5+1));
  # 与 ssh_auto.py 部署调用的 120 秒超时对齐, 等待有界, 不会无限拖住部署会话。
  for ((attempt=1; attempt<=HEALTH_RETRIES; attempt++)); do
    if probe_health; then return 0; fi
    if (( attempt < HEALTH_RETRIES )); then sleep "$HEALTH_INTERVAL"; fi
  done
  return 1
}

rollback() {
  local reason="$1"
  echo "== ROLLBACK: $reason =="
  if [[ -z "$BACKUP_BIN" ]]; then echo "DEPLOY_ROLLED_BACK reason=no_previous_binary"; return 1; fi
  if ! run_service stop; then echo "rollback stop failed"; fi
  if [[ "$DRY_RUN" == 1 ]]; then printf '[dry-run] install %s -> %s\n' "$BACKUP_BIN" "$CURRENT_BIN"; elif ! cp -f "$BACKUP_BIN" "$CURRENT_BIN"; then echo "rollback binary restore failed"; fi
  if ! run_service start; then echo "rollback start failed"; fi
  # 回滚后要断言的是"**旧**二进制起来了", 不是"新版本起来了" —— 后者永远不可能成立,
  # 会让每一次成功的回滚都打印一行 "rollback health check failed", 把好事说成坏事。
  # 旧版本号在替换前已从现行二进制读进 $version; 若当时读不出来(降级成时间戳), 就只断言 status/db。
  if [[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then EXPECTED_VERSION="$version"; else EXPECTED_VERSION=""; fi
  if wait_for_health; then echo "rollback health check passed (version=$HEALTH_VERSION)"; else echo "rollback health check failed"; fi
  echo "DEPLOY_ROLLED_BACK reason=$reason"
  return 1
}

inspect_database() {
  local db_file="${SWITCHYARD_DB_PATH:-/opt/switchyard/data/data.db}"
  python3 - "$db_file" <<'PY'
import sqlite3, sys
try: conn = sqlite3.connect(f"file:{sys.argv[1]}?mode=ro", uri=True)
except Exception as exc:
    print(f"DATABASE_UNAVAILABLE ({type(exc).__name__})"); raise SystemExit(0)
for label, table, keys in (("probe columns", "channel_keys", ("probe",)), ("cost columns", "relay_requests", ("supply_cost", "user_charge", "profit"))):
    print(f"== SQLITE: {label} ==")
    try:
        cols = [r[1] for r in conn.execute(f"PRAGMA table_info({table})")]
        cols = [c for c in cols if any(k in c for k in keys)]
        print(" ".join(cols) if cols else "(none)")
    except Exception as exc: print(f"UNAVAILABLE ({type(exc).__name__})")
print("== SQLITE: quality weight settings ==")
try:
    rows = conn.execute("SELECT key FROM settings WHERE key LIKE 'quality_weight%'").fetchall()
    print(" ".join(str(r[0]) for r in rows) if rows else "(none)")
except Exception as exc: print(f"UNAVAILABLE ({type(exc).__name__})")
conn.close()
PY
}

if [[ ! -s "$NEW_BIN" ]]; then echo "NO_NEW_BINARY: $NEW_BIN"; exit 1; fi
echo "== NEW BINARY =="; ls -lh "$NEW_BIN"
if [[ ! -e "$CURRENT_BIN" ]]; then echo "NO_PREVIOUS_BINARY: $CURRENT_BIN"; exit 1; fi
version=unknown
if [[ "$DRY_RUN" != 1 && -x "$CURRENT_BIN" ]]; then
  # version 子命令输出多行 Key: value, 不能把整段输出直接当裸版本号匹配; 只提取 Version: 行。
  candidate="$($CURRENT_BIN version 2>/dev/null | sed -n 's/^[[:space:]]*Version:[[:space:]]*\(v[0-9][0-9.]*\)[[:space:]]*$/\1/p' | head -1 || true)"
  if [[ "$candidate" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then version="$candidate"; fi
fi
[[ "$version" == unknown ]] && version="v$(date +%Y%m%d%H%M%S)"
BACKUP_BIN="${BIN_DIR}/switchyard.prev-${version}"
if [[ -e "$BACKUP_BIN" && "$DRY_RUN" != 1 ]]; then
  # 不覆盖同名备份: 名字里的版本说的是"被替换下去的那个二进制", 而这次要备份的可能不是它
  # (例如回退后又重新部署同一版本)。覆盖会让备份的名字与内容不符, 等回滚时才发现就晚了。
  BACKUP_BIN="${BIN_DIR}/switchyard.prev-${version}-$(date +%Y%m%d%H%M%S)"
fi
if [[ "$DRY_RUN" == 1 ]]; then
  printf '[dry-run] systemctl stop %s\n' "$SERVICE"
  printf '[dry-run] cp -f %s %s\n' "$CURRENT_BIN" "$BACKUP_BIN"
  printf '[dry-run] chmod 755 %s && mv -f %s %s\n' "$NEW_BIN" "$NEW_BIN" "$CURRENT_BIN"
else
  run_service stop
  cp -f "$CURRENT_BIN" "$BACKUP_BIN"
  # 二进制备份轮转: 每次部署产生一份 switchyard.prev-*(约 53MB), 不清理会随部署次数线性增长。
  # 保留最近 10 份 —— 覆盖近两周的部署历史, 回滚窗口足够; 按修改时间取 newest 之外的部分删除。
  # 只匹配 switchyard.prev-* 自己产生的前缀, 不碰运维手工放的其它文件。
  ls -1t "$BIN_DIR"/switchyard.prev-* 2>/dev/null | tail -n +11 | while IFS= read -r old; do
    rm -f -- "$old"
  done
  chmod 755 "$NEW_BIN"
  mv -f "$NEW_BIN" "$CURRENT_BIN"
fi
if [[ "$DRY_RUN" == 1 ]]; then
  printf '[dry-run] systemctl start %s\n' "$SERVICE"
else
  # start 失败必须走回滚, 不能让 set -e 把脚本直接带走: 那样新二进制留在原地、服务是停的,
  # 却没有 DEPLOY_ROLLED_BACK, 也没有恢复旧版本。StartLimitBurst 触发时正是这个形状(unit 进入 failed)。
  if ! run_service start; then rollback start_failed; fi
fi
health_reason=
if [[ "$DRY_RUN" != 1 ]]; then if ! active="$(systemctl is-active "$SERVICE")" || [[ "$active" != active ]]; then health_reason=service_not_active; fi; fi
# dry-run 不探活: 此刻响应 /healthz 的是**现行**进程, 拿它当作"新二进制健康"的证据是假信号。
if [[ -z "$health_reason" && "$DRY_RUN" != 1 ]] && ! wait_for_health; then health_reason=health_check_failed; fi
if [[ -n "$health_reason" ]]; then rollback "$health_reason"; fi
if [[ "$DRY_RUN" == 1 ]]; then DEPLOY_VERSION="${EXPECTED_VERSION:-dry-run}"; else DEPLOY_VERSION="$HEALTH_VERSION"; fi
inspect_database
if [[ -n "$EXPECTED_VERSION" && "$DEPLOY_VERSION" != "$EXPECTED_VERSION" ]]; then rollback version_mismatch; fi
printf 'DEPLOY_OK version=%s\n' "$DEPLOY_VERSION"
