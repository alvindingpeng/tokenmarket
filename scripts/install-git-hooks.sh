#!/bin/bash
# install-git-hooks.sh: 安装 post-commit 与 auto-push 钩子。
#
# 为什么需要这个脚本: .git/hooks/ 不在版本控制里。钩子丢失后, "推送 + 发版"
# 会静默失效 —— 没有报错, 只是再也不发生。把源文件放进 scripts/ 并给出安装入口,
# 至少让"重装"是一步可查的动作, 而不是靠记忆重建。
#
# 钩子目录用 `git rev-parse --git-path hooks` 解析: 在 git worktree 里 .git 是文件,
# 写死 $ROOT/.git/hooks 会装到一半失败, 或者装进一个根本不存在的目录。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HOOKS_DIR="$(cd "$ROOT" && git rev-parse --git-path hooks)"
case "$HOOKS_DIR" in
  /*) ;;
  *) HOOKS_DIR="$ROOT/$HOOKS_DIR" ;;
esac

if [[ ! -d "$HOOKS_DIR" ]]; then
    echo "install-git-hooks.sh: $HOOKS_DIR 不存在(不是 git 工作区?)" >&2
    exit 1
fi

# auto-push.sh 是钩子的实现体, post-commit 是入口。两者必须同时存在,
# 否则入口会 nohup 一个不存在的脚本然后静默失败。
install -m 0755 "$ROOT/scripts/auto-push.sh" "$HOOKS_DIR/auto-push.sh"

cat > "$HOOKS_DIR/post-commit" <<'EOS'
#!/bin/bash
# post-commit: 每次提交后实时推送到 GitHub tokenmarket, 版本号变化时顺带发布 Release。
# 异步执行不阻塞 commit; 失败只写 git common dir 下的 auto-push.log 与 publish-release.log,
# 不阻断提交。钩子路径用 git rev-parse 解析, 以兼容 git worktree。
REPO="$(git rev-parse --show-toplevel)"
BRANCH="$(git rev-parse --abbrev-ref HEAD)"
HOOKS_DIR="$(git rev-parse --git-path hooks)"
case "$HOOKS_DIR" in /*) ;; *) HOOKS_DIR="$REPO/$HOOKS_DIR" ;; esac
git remote get-url github >/dev/null 2>&1 || exit 0
nohup bash "$HOOKS_DIR/auto-push.sh" "$REPO" "$BRANCH" >/dev/null 2>&1 &
disown
exit 0
EOS
chmod 0755 "$HOOKS_DIR/post-commit"

echo "installed: $HOOKS_DIR/auto-push.sh"
echo "installed: $HOOKS_DIR/post-commit"
