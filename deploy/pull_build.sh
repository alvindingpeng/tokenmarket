set -e
export PATH="$PATH:/usr/local/go/bin"
export GOPROXY=https://goproxy.cn,direct
cd /opt/switchyard/src/switchyard
echo "== GIT PULL =="
git pull --ff-only origin master
git log --oneline -3

# 版本信息必须注入（否则 conf.Version="dev", 管理后台弹版本错误告警）
# 与 scripts/build-local.sh 同口径: 版本取 main.go "// Version" 标记
VERSION=$(grep -oP '^// Version \Kv[0-9.]+' main.go)
COMMIT=$(git rev-parse --short HEAD)
BUILD_TIME=$(TZ='Asia/Shanghai' date +'%F %T %z')
MODULE='github.com/alvindingpeng/switchyard'
LDFLAGS="-X '${MODULE}/internal/conf.Version=${VERSION}' -X '${MODULE}/internal/conf.Commit=${COMMIT}' -X '${MODULE}/internal/conf.BuildTime=${BUILD_TIME}' -s -w"
echo "== BUILD ${VERSION} (${COMMIT}) START $(date +%T) =="
go build -trimpath -tags=jsoniter -ldflags="${LDFLAGS}" -o /opt/switchyard/bin/switchyard.new .
echo "== BUILD DONE $(date +%T) =="
ls -lh /opt/switchyard/bin/switchyard.new
/opt/switchyard/bin/switchyard.new version || true
echo "BUILD_OK"
