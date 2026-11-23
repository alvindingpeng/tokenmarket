#!/bin/bash
# 代码已推送后触发发版。GitHub Actions 测试通过后才构建并发布 Release。
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
unset GIT_DIR GIT_WORK_TREE
VERSION="$(bash scripts/version.sh)"
if [[ ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "Invalid release version: $VERSION" >&2
    exit 1
fi
if ! awk -v version="$VERSION" '$1 == "##" && $2 == version { found=1 } END { exit !found }' CHANGELOG.md; then
    echo "Missing changelog entry for $VERSION" >&2
    exit 1
fi

BRANCH="$(git symbolic-ref --short HEAD)"
REMOTE="${SWITCHYARD_RELEASE_REMOTE:-github}"
if ! git remote get-url "$REMOTE" >/dev/null 2>&1; then REMOTE=origin; fi
HEAD_SHA="$(git rev-parse HEAD)"
REMOTE_BRANCH="$(git ls-remote "$REMOTE" "refs/heads/$BRANCH" | cut -f1)"
if [[ "$REMOTE_BRANCH" != "$HEAD_SHA" ]]; then
    echo "Push the current commit to $REMOTE/$BRANCH before releasing $VERSION" >&2
    exit 1
fi
if [[ "$BRANCH" == stable ]]; then
    echo "The stable push triggers GitHub Actions for $VERSION"
    exit 0
fi

TAG="refs/tags/$VERSION"
if ! git show-ref --verify --quiet "$TAG"; then
    git tag -a "$VERSION" -m "release $VERSION"
fi
if [[ "$(git rev-parse "$TAG^{commit}")" != "$HEAD_SHA" ]]; then
    echo "Tag $VERSION points to a different commit" >&2
    exit 1
fi
# Git rejects overwriting an existing remote tag. GitHub Actions owns the Release.
git push "$REMOTE" "$TAG:$TAG"
