#!/bin/bash
# auto-push.sh: 提交后把当前提交推送到 GitHub, 并在版本号变化时发布 Release。
#
# 本文件是 .git/hooks/auto-push.sh 的**受版本控制副本**。钩子本身不在 git 里,
# 换机器/重克隆就丢; 丢了以后"自动推送 + 自动发版"静默失效, 且没有任何报错。
# 安装: bash scripts/install-git-hooks.sh
#
# 关键约束(2026-11-20 的教训):
#   Release 必须以后面的代码推送成功为前提。无条件发布时, 代码 push 被 fast-forward
#   拒绝而标签推送不受同样限制, 于是标签 + Release 指向了仓库里不可达的提交。
#   标签是"某提交在本仓库可达"的声明; 代码没上去, 这个声明就是假的。
#
# 目标分支取自提交所在的分支(post-commit 传入), 不硬编码: 远端 main 自 v1.5.6 起
# 就与 master 分叉, 硬编码 main 会让每次自动推送都以 non-fast-forward 失败。
#
# 工作树兼容: 本仓库用 git worktree 并行开发(octopus / octopus-baseline 共用一套
# 对象库), 工作树里的 .git 是一个**文件**而不是目录。所以钩子路径必须用
# `git rev-parse --git-path hooks` 解析, 日志写进 git common dir,
# 不能写 $REPO/.git/... —— 那样在工作树里必然指向一个不存在的路径,
# 而钩子的失败是静默的: 提交照旧成功, 只是推送再也没发生过。
REPO="$1"
BRANCH="$2"
[ -n "$REPO" ] || exit 1
cd "$REPO" || exit 1

# 变量名不能叫 GIT_DIR: 钩子是被 git 调起的, GIT_DIR 是 git 自己的环境变量,
# 在这里重新赋值会把它**指向 common dir**, 随后的 git rev-parse HEAD / git push HEAD
# 全都解析成 common dir 上的那个分支 —— 输出的不是当前工作树的提交。
# 这条恰好把 SaaS 树的提交当成基线版推了上去(与文件头警告的历史事故同一个形态)。
COMMON_DIR="$(git rev-parse --git-common-dir)"
[ -d "$COMMON_DIR" ] || COMMON_DIR="$REPO/.git"
case "$COMMON_DIR" in /*) ;; *) COMMON_DIR="$REPO/$COMMON_DIR" ;; esac
LOG="$COMMON_DIR/auto-push.log"

# 显式清掉 git 传进来的 GIT_DIR, 保证下面所有 git 命令都按**当前工作树**解析。
unset GIT_DIR GIT_WORK_TREE

# 分离头指针时拿不到分支名: 回落到 master, 而不是推出一个名为 HEAD 的分支。
TARGET="${BRANCH:-master}"
if [ -z "$TARGET" ] || [ "$TARGET" = "HEAD" ]; then
  TARGET=master
fi

PUSHED=0
for i in 1 2 3; do
  if git push github "HEAD:refs/heads/$TARGET" >/tmp/autopush.out 2>&1; then
    echo "$(date -Iseconds) OK $TARGET $(git rev-parse --short HEAD) -> $TARGET" >> "$LOG"
    PUSHED=1
    break
  fi
  if [ "$i" = 3 ]; then
    echo "$(date -Iseconds) FAIL $TARGET $(git rev-parse --short HEAD): $(tail -2 /tmp/autopush.out | tr '\n' ' ')" >> "$LOG"
  fi
  sleep 5
done

# 发布 Release 必须以后面的代码推送成功为前提(见文件头)。
if [ "$PUSHED" != 1 ]; then
  echo "$(date -Iseconds) SKIP publish $(git rev-parse --short HEAD): branch push to $TARGET failed, tag/release would point to an unreachable commit" >> "$LOG"
  exit 0
fi

# 发布 Release: 脚本自身幂等, 版本没有对应 CHANGELOG 章节或取不到 token 时静默跳过。
if [ -f "$REPO/scripts/publish-release.sh" ]; then
  bash "$REPO/scripts/publish-release.sh" >/dev/null 2>&1
fi
exit 0
