#!/bin/bash
# 发布当前版本到 GitHub: 打标签(如缺) + 推送标签 + 用 CHANGELOG.md 对应章节创建/更新 Release。
#
# 由 .git/hooks/post-commit 经由 scripts/publish-release.sh 调用, 也可手工执行:
#   GITHUB_TOKEN=... bash scripts/publish-release.sh
# GITHUB_TOKEN 缺省时从 origin=github 的远端 URL 里取(该 URL 已内嵌 PAT)。
# 幂等: 标签已存在则不重复打; Release 已存在则用最新正文覆盖(便于改了 CHANGELOG 再跑一次)。
set -uo pipefail

readonly REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}" || exit 1

readonly VERSION="$(bash scripts/version.sh)" || exit 1
readonly LOG="${REPO_ROOT}/.git/publish-release.log"
say() { echo "$(date -Iseconds) $*" | tee -a "${LOG}" >&2; }

# 目标仓库: 优先 GITHUB_REPOSITORY 环境变量, 否则解析 github 远端 URL。
OWNER_REPO="${GITHUB_REPOSITORY:-}"
if [ -z "${OWNER_REPO}" ]; then
    URL="$(git remote get-url github 2>/dev/null)" || { say "SKIP 无 github 远端"; exit 0; }
    OWNER_REPO="$(printf '%s' "${URL}" | sed -n 's|.*github\.com[/:]\([^/]*/[^/]*\).git$|\1|p')"
    [ -n "${OWNER_REPO}" ] || { say "SKIP 无法从远端 URL 解析仓库"; exit 0; }
fi

TOKEN="${GITHUB_TOKEN:-}"
if [ -z "${TOKEN}" ]; then
    TOKEN="$(git remote get-url github 2>/dev/null | sed -n 's|https://[^@]*@.*|&|p' | sed 's|https://||; s|@.*||')"
fi
[ -n "${TOKEN}" ] || { say "SKIP 没有可用的 GITHUB_TOKEN"; exit 0; }

# 1) 标签: 不存在则创建并推送。
if ! git rev-parse -q --verify "refs/tags/${VERSION}" >/dev/null; then
    git tag -a "${VERSION}" -m "release ${VERSION}" || { say "FAIL 打标签 ${VERSION}"; exit 1; }
fi
SHA="$(git rev-parse "refs/tags/${VERSION}^{commit}")"
REMOTE_SHA="$(git ls-remote github "refs/tags/${VERSION}" 2>/dev/null | awk '{print $1}')"
if [ -z "${REMOTE_SHA}" ]; then
    git push github "refs/tags/${VERSION}" >/dev/null 2>&1 || { say "FAIL 推送标签 ${VERSION}"; exit 1; }
fi

# 2) 正文: 取 CHANGELOG.md 中 "## <VERSION>" 到下一个 "## " 之间的内容。
if command -v python3 >/dev/null 2>&1; then
    BODY_FILE="$(mktemp)"
    python3 - "${VERSION}" CHANGELOG.md "${BODY_FILE}" <<'PY'
import re, sys
version, src, dst = sys.argv[1], sys.argv[2], sys.argv[3]
text = open(src, encoding='utf-8').read()
m = re.search(r'^## ' + re.escape(version) + r'.*$', text, re.M)
body = ''
if m:
    rest = text[m.end():]
    nxt = re.search(r'^## ', rest, re.M)
    body = (rest[:nxt.start()] if nxt else rest).strip()
if not body:
    sys.exit(3)
open(dst, 'w', encoding='utf-8').write(body + '\n')
PY
    if [ $? -ne 0 ]; then say "SKIP CHANGELOG.md 里没有 ${VERSION} 章节"; rm -f "${BODY_FILE}"; exit 0; fi
else
    say "SKIP 缺少 python3, 无法解析 CHANGELOG"; exit 0
fi

STATUS="$(python3 - "${OWNER_REPO}" "${VERSION}" "${TOKEN}" "${BODY_FILE}" "${SHA}" <<'PY'
import json, sys, urllib.request, urllib.error
repo, version, token, body_file, sha = sys.argv[1:6]
body = open(body_file, encoding='utf-8').read()
api = 'https://api.github.com/repos/' + repo

def req(method, url, payload=None):
    data = json.dumps(payload).encode() if payload is not None else None
    r = urllib.request.Request(url, data=data, method=method, headers={
        'Authorization': 'Bearer ' + token,
        'Accept': 'application/vnd.github+json',
        'User-Agent': 'octopus-publish-release',
        'X-GitHub-Api-Version': '2022-11-28',
    })
    try:
        with urllib.request.urlopen(r, timeout=30) as resp:
            raw = resp.read()
            return resp.status, (json.loads(raw) if raw else {})
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode('utf-8', 'replace')

status, info = req('GET', api + '/releases/tags/' + version)
data = {'tag_name': version, 'name': version, 'body': body,
        'draft': False, 'prerelease': False, 'target_commitish': sha}
if status == 200:
    code, resp = req('PATCH', api + '/releases/' + str(info['id']), data)
else:
    code, resp = req('POST', api + '/releases', data)
print(code, resp.get('html_url', resp) if isinstance(resp, dict) else resp)
sys.exit(0 if code in (200, 201) else 1)
PY
)"
RC=$?
rm -f "${BODY_FILE}"
say "RELEASE ${VERSION} ${STATUS}"
exit ${RC}
