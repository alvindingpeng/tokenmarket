#!/usr/bin/env bash
# 外部探活: 脚本失败也必须返回 0, 不能让 systemd timer 停止。
set -u
HEALTH_URL="${SWITCHYARD_HEALTH_URL:-http://127.0.0.1:8088/healthz}"
LOG_FILE="${SWITCHYARD_WATCHDOG_LOG:-/opt/switchyard/data/watchdog-failures.log}"
RESTART_FILE="${SWITCHYARD_WATCHDOG_RESTART_FILE:-/opt/switchyard/data/watchdog-last-restart}"
DRY_RUN="${SWITCHYARD_WATCHDOG_DRY_RUN:-0}"

log_line() {
  local state="$1" detail="$2" stamp
  stamp="$(date --rfc-3339=seconds 2>/dev/null | tr ' ' 'T')"
  if [[ -z "$stamp" ]]; then stamp="$(date '+%Y-%m-%dT%H:%M:%S%:z')"; fi
  if ! mkdir -p "$(dirname "$LOG_FILE")" 2>/dev/null || ! printf '%s %s %s\n' "$stamp" "$state" "$detail" >>"$LOG_FILE" 2>/dev/null; then
    # 记录系统级日志作兜底, 并始终让 timer 正常结束。
    logger -t switchyard-watchdog "log_write_failed state=$state" 2>/dev/null || :
  fi
}

last_state=""
if [[ -r "$LOG_FILE" ]]; then
  last_state="$(awk 'NF >= 2 { state=$2 } END { print state }' "$LOG_FILE" 2>/dev/null)"
fi
body="$(mktemp 2>/dev/null)" || body=""
http_code=000
curl_exit=0
if [[ -n "$body" ]]; then
  if http_code="$(curl -sS --max-time 5 -o "$body" -w '%{http_code}' "$HEALTH_URL" 2>/dev/null)"; then curl_exit=0; else curl_exit=$?; fi
else
  curl_exit=1
fi
if [[ "$curl_exit" -eq 0 && "$http_code" == 200 ]] && parsed="$(python3 - "$body" <<'PY'
import json, sys
try:
    with open(sys.argv[1], encoding="utf-8") as f: data=json.load(f)
except Exception:
    raise SystemExit(1)
if data.get("status") != "ok" or data.get("db") != "ok": raise SystemExit(1)
print(data.get("db", "")); print(data.get("version", ""))
PY
)"; then
  db_state="$(printf '%s\n' "$parsed" | sed -n '1p')"
  version="$(printf '%s\n' "$parsed" | sed -n '2p')"
  if [[ "$last_state" != ok ]]; then log_line ok "http=200 db=$db_state version=$version"; fi
  [[ -n "$body" ]] && rm -f "$body"
  exit 0
fi
[[ -n "$body" ]] && rm -f "$body"
log_line fail "http=$http_code curl_exit=$curl_exit"

# 统计最后一条 ok/restart 之后的 fail 行数(包含本次刚追加的 fail)。
consecutive="$(awk '$2=="ok" || $2=="restart" {n=0; next} $2=="fail" {n++} END {print n+0}' "$LOG_FILE" 2>/dev/null)"
if [[ "$consecutive" -ge 3 ]]; then
  can_restart=1
  if [[ -e "$RESTART_FILE" ]]; then
    now_epoch="$(date +%s 2>/dev/null)"; restart_epoch="$(stat -c %Y "$RESTART_FILE" 2>/dev/null)" || restart_epoch=0
    if [[ "$now_epoch" =~ ^[0-9]+$ && "$restart_epoch" =~ ^[0-9]+$ ]] && (( now_epoch - restart_epoch <= 600 )); then can_restart=0; fi
  fi
  if [[ "$can_restart" == 1 ]]; then
    if [[ "$DRY_RUN" == 1 ]]; then
      log_line restart "consecutive_failures=$consecutive"
    elif systemctl restart switchyard >/dev/null 2>&1; then
      log_line restart "consecutive_failures=$consecutive"
    else
      log_line fail "http=$http_code curl_exit=$curl_exit restart_exit=1"
    fi
    if ! mkdir -p "$(dirname "$RESTART_FILE")" 2>/dev/null || ! touch "$RESTART_FILE" 2>/dev/null; then logger -t switchyard-watchdog restart_mtime_write_failed 2>/dev/null || :; fi
  fi
fi
exit 0