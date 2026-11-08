#!/usr/bin/env bash
set -euo pipefail
SHARDS="${SHARDS:-4}"
TIMEOUT="${TIMEOUT:-5m}"
RACE="${RACE:-0}"
SHARD_INDEX="${SHARD_INDEX:-}"
GO_PACKAGES="${GO_PACKAGES:-./internal/...}"
if ! [[ "$SHARDS" =~ ^[0-9]+$ ]] || (( SHARDS < 1 )); then echo "SHARDS must be a positive integer" >&2; exit 2; fi
if [[ -n "$SHARD_INDEX" ]] && { ! [[ "$SHARD_INDEX" =~ ^[0-9]+$ ]] || (( SHARD_INDEX >= SHARDS )); }; then echo "SHARD_INDEX out of range" >&2; exit 2; fi
args=(-count=1 -timeout "$TIMEOUT")
if [[ "$RACE" == 1 ]]; then args+=(-race); fi
if [[ -n "${EXTRA_ARGS:-}" ]]; then read -r -a extra <<< "$EXTRA_ARGS"; args+=("${extra[@]}"); fi
work=$(mktemp -d); trap 'rm -rf "$work"' EXIT
# Weights are measured package wall times from the 6-core baseline: op=38.6s,
# server/handlers=16.9s, relay=12.1s, db=3.3s, server/middleware=1.1s; others <1s.
# Unlisted packages use test-count * 0.1s as an estimate.
go list -f '{{.ImportPath}} {{len .TestGoFiles}} {{len .XTestGoFiles}}' "$GO_PACKAGES" | awk '$2+$3>0 {print $1}' | sort > "$work/packages"
: > "$work/tests.tsv"
while IFS= read -r pkg; do
  go test -list '^Test' "$pkg" | awk -v p="$pkg" '/^Test[^[:space:]]*$/ {print p "\t" $0}' >> "$work/tests.tsv"
done < "$work/packages"
[[ -s "$work/tests.tsv" ]] || { echo "no tests under $GO_PACKAGES" >&2; exit 1; }
python3 - "$work/tests.tsv" "$work/packages" "$work/plan.tsv" "$SHARDS" <<'PY'
import hashlib, sys
tests_path, packages_path, plan_path, shards = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
rows = [line.rstrip("\n").split("\t", 1) for line in open(tests_path)]
by_pkg = {}
for pkg, name in rows: by_pkg.setdefault(pkg, []).append(name)
fixed = {"/internal/op":38.6, "/internal/server/handlers":16.9, "/internal/relay":12.1, "/internal/db":3.3, "/internal/server/middleware":1.1}
def weight(pkg):
    for suffix, value in fixed.items():
        if pkg.endswith(suffix): return value
    return len(by_pkg[pkg]) * .1
pkgs = sorted(by_pkg, key=lambda p:(-weight(p), -len(by_pkg[p]), p))
weights = {p:weight(p) for p in pkgs}; threshold=sum(weights.values())/shards*.8
loads=[0.0]*shards; plan=[]
# Split heavy packages across all shards first; stable sha1(package + test name) modulo N.
for pkg in pkgs:
    if weights[pkg] <= threshold: continue
    buckets=[[] for _ in range(shards)]
    for name in sorted(set(by_pkg[pkg])):
        i=int(hashlib.sha1((pkg+name).encode()).hexdigest()[:8],16)%shards
        buckets[i].append(name)
    for i,names in enumerate(buckets):
        if names:
            plan.append((i,pkg,"|".join(names)))
            loads[i]+=weights[pkg]*len(names)/len(by_pkg[pkg])
# Greedily assign ordinary packages in stable weight order to current lightest shard.
for pkg in pkgs:
    if weights[pkg] > threshold: continue
    i=min(range(shards),key=lambda j:(loads[j],j)); plan.append((i,pkg,"")); loads[i]+=weights[pkg]
with open(plan_path,"w") as f:
    for i,pkg,regex in sorted(plan): f.write(f"{i}\t{pkg}\t{regex}\n")
print(f"test-go: packages={len(by_pkg)} tests={len(rows)} shards={shards} threshold={threshold:.1f}s estimated_loads="+",".join(f"{x:.1f}" for x in loads))
PY
run_shard() {
  local idx=$1
  local -a pkgs=()
  local s pkg regex has=0 started
  while IFS=$'\t' read -r s pkg regex; do
    [[ "$s" == "$idx" ]] || continue; has=1
    if [[ -z "$regex" ]]; then pkgs+=("$pkg"); fi
  done < "$work/plan.tsv"
  if (( ${#pkgs[@]} )); then
    echo "shard $((idx+1)): whole packages ${pkgs[*]}"
    started=$SECONDS
    go test "${args[@]}" "${pkgs[@]}"
    echo "shard $((idx+1)): whole-package compile+run $((SECONDS-started))s"
  fi
  while IFS=$'\t' read -r s pkg regex; do
    [[ "$s" == "$idx" && -n "$regex" ]] || continue; has=1
    echo "shard $((idx+1)): split package $pkg"
    started=$SECONDS
    go test "${args[@]}" -run "^(${regex})$" "$pkg"
    echo "shard $((idx+1)): $pkg compile+run $((SECONDS-started))s"
  done < "$work/plan.tsv"
  if (( has == 0 )); then echo "shard $((idx+1)): no tests, skipping"; fi
}
if [[ -n "$SHARD_INDEX" ]]; then run_shard "$SHARD_INDEX"; else for ((i=0;i<SHARDS;i++)); do run_shard "$i"; done; fi
