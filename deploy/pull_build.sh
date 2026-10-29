set -e
export PATH="$PATH:/usr/local/go/bin"
export GOPROXY=https://goproxy.cn,direct
cd /opt/switchyard/src/switchyard
echo "== GIT PULL =="
git pull --ff-only origin master
git log --oneline -3
echo "== BUILD START $(date +%T) =="
go build -o /opt/switchyard/bin/switchyard.new .
echo "== BUILD DONE $(date +%T) =="
ls -lh /opt/switchyard/bin/switchyard.new
echo "BUILD_OK"
