echo "== 二进制信息 =="
ls -lh /opt/switchyard/bin/switchyard
/opt/switchyard/bin/switchyard version
echo ""
echo "== 服务状态 =="
systemctl is-active switchyard
echo ""
echo "== 检查前端资源是否打包进二进制（embed 检查）=="
strings /opt/switchyard/bin/switchyard | grep -E "grid-cols-[0-9]|md:grid-cols" | head -10
echo ""
echo "== 或者直接查看是否有独立前端目录 =="
ls -la /opt/switchyard/static/ 2>/dev/null || echo "无独立 static 目录（前端应该 embed 进二进制）"
echo "CHECK_DONE"
