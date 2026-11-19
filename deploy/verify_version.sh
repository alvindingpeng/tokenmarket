echo "== /healthz =="
curl -s --max-time 5 http://127.0.0.1:8088/healthz | head -c 500
echo ""
echo "== /api/v1/update/now-version =="
curl -s --max-time 5 http://127.0.0.1:8088/api/v1/update/now-version | head -c 300
echo ""
echo "== /metrics build_info =="
curl -s --max-time 5 http://127.0.0.1:8088/metrics | grep switchyard_build_info | head -3
echo "== service =="
systemctl is-active switchyard
echo "VERSION_VERIFY_DONE"
