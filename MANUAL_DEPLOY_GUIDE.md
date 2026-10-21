# Switchyard v0.40.0 手动部署指南

## 📋 部署信息

- **服务器**: tt.xjj.se
- **用户**: root
- **密码**: kLaRns0425
- **服务**: switchyard.service
- **二进制路径**: /usr/local/bin/switchyard
- **本地构建**: build/switchyard-local
- **版本**: v0.40.0 (commit 70135ba)
- **SHA-256**: 5fce5ee0f53d518793d8b6b9313a5e04924d64a11eae245d6414f8ad7b7a5e2e

## 🚀 部署步骤

### 1. 上传二进制文件

在本地执行：

```bash
# 从 octopus 目录执行
scp build/switchyard-local root@tt.xjj.se:/usr/local/bin/switchyard.new
# 密码: kLaRns0425
```

### 2. 连接到服务器

```bash
ssh root@tt.xjj.se
# 密码: kLaRns0425
```

### 3. 在服务器上执行部署

复制以下命令块，在服务器上一次性执行：

```bash
# ============================================================
# Switchyard v0.40.0 部署脚本
# ============================================================

set -e

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}🚀 开始部署 Switchyard v0.40.0${NC}"
echo ""

# 1. 验证上传的文件
echo -e "${YELLOW}📋 验证文件完整性...${NC}"
sha256sum /usr/local/bin/switchyard.new
EXPECTED_SHA="5fce5ee0f53d518793d8b6b9313a5e04924d64a11eae245d6414f8ad7b7a5e2e"
ACTUAL_SHA=$(sha256sum /usr/local/bin/switchyard.new | cut -d' ' -f1)

if [ "$ACTUAL_SHA" != "$EXPECTED_SHA" ]; then
    echo -e "${RED}❌ SHA-256 不匹配！${NC}"
    echo "期望: $EXPECTED_SHA"
    echo "实际: $ACTUAL_SHA"
    exit 1
fi
echo -e "${GREEN}✓ 哈希验证通过${NC}"
echo ""

# 2. 检查当前服务状态
echo -e "${YELLOW}📊 当前服务状态:${NC}"
systemctl status switchyard.service --no-pager | head -5
echo ""
curl -s http://127.0.0.1:8088/healthz | jq . || echo "健康检查不可用"
echo ""

# 3. 暂停流量（如果 API 可用）
echo -e "${YELLOW}⏸️  暂停流量...${NC}"
curl -s -X POST http://127.0.0.1:8088/admin/traffic-control -d 'paused=true' 2>/dev/null || echo "流量控制 API 不可用，跳过"
sleep 2
echo ""

# 4. 停止服务
echo -e "${YELLOW}🛑 停止服务...${NC}"
systemctl stop switchyard.service
sleep 2
echo -e "${GREEN}✓ 服务已停止${NC}"
echo ""

# 5. 备份当前版本和数据库
echo -e "${YELLOW}💾 备份当前版本...${NC}"
TIMESTAMP=$(date +%Y%m%d-%H%M%S)
mkdir -p /var/backups/switchyard

if [ -f /usr/local/bin/switchyard ]; then
    cp /usr/local/bin/switchyard /var/backups/switchyard/switchyard-${TIMESTAMP}
    echo "   ✓ 二进制已备份"
fi

if [ -f /var/lib/switchyard/switchyard.db ]; then
    cp /var/lib/switchyard/switchyard.db /var/backups/switchyard/switchyard-${TIMESTAMP}.db
    echo "   ✓ 数据库已备份"
fi

echo -e "${GREEN}✓ 备份完成: /var/backups/switchyard/switchyard-${TIMESTAMP}${NC}"
echo ""

# 6. 部署新版本
echo -e "${YELLOW}🚢 部署新版本...${NC}"
mv /usr/local/bin/switchyard.new /usr/local/bin/switchyard
chmod +x /usr/local/bin/switchyard
echo -e "${GREEN}✓ 二进制已部署${NC}"
echo ""

# 7. 启动服务
echo -e "${YELLOW}▶️  启动服务...${NC}"
systemctl start switchyard.service
sleep 3
echo -e "${GREEN}✓ 服务已启动${NC}"
echo ""

# 8. 验证服务状态
echo -e "${YELLOW}🔍 验证服务状态...${NC}"
systemctl status switchyard.service --no-pager | head -10
echo ""

# 9. 验证健康状态
echo -e "${YELLOW}🏥 健康检查...${NC}"
sleep 2
HEALTHZ=$(curl -s http://127.0.0.1:8088/healthz)
echo "$HEALTHZ" | jq .

STATUS=$(echo "$HEALTHZ" | jq -r '.status' 2>/dev/null || echo "unknown")
DB_STATUS=$(echo "$HEALTHZ" | jq -r '.db' 2>/dev/null || echo "unknown")

if [ "$STATUS" != "ok" ] || [ "$DB_STATUS" != "ok" ]; then
    echo ""
    echo -e "${RED}❌ 健康检查失败！正在回滚...${NC}"
    systemctl stop switchyard.service
    cp /var/backups/switchyard/switchyard-${TIMESTAMP} /usr/local/bin/switchyard
    systemctl start switchyard.service
    echo -e "${YELLOW}✓ 已回滚到之前的版本${NC}"
    exit 1
fi
echo ""

# 10. 恢复流量
echo -e "${YELLOW}▶️  恢复流量...${NC}"
curl -s -X POST http://127.0.0.1:8088/admin/traffic-control -d 'paused=false' 2>/dev/null || echo "流量控制 API 不可用，跳过"
echo ""

# 11. 确认版本
echo -e "${YELLOW}📌 确认版本...${NC}"
/usr/local/bin/switchyard version
echo ""

# 12. 最终检查
echo -e "${YELLOW}🔍 最终检查...${NC}"
echo "服务状态:"
systemctl is-active switchyard.service
echo ""
echo "监听端口:"
netstat -tlnp | grep switchyard || ss -tlnp | grep switchyard
echo ""

echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${GREEN}🎉 部署成功！Switchyard v0.40.0 已上线${NC}"
echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""
echo "备份位置: /var/backups/switchyard/switchyard-${TIMESTAMP}"
echo ""
echo "查看日志: journalctl -u switchyard.service -f"
echo ""
```

### 4. 验证部署

部署完成后，验证以下内容：

```bash
# 检查服务状态
systemctl status switchyard.service

# 检查健康状态
curl http://127.0.0.1:8088/healthz | jq .

# 查看实时日志
journalctl -u switchyard.service -f

# 检查版本
/usr/local/bin/switchyard version
```

期望输出：
- 服务状态: active (running)
- 健康检查: {"status":"ok","db":"ok",...}
- 版本: v0.40.0 (70135ba...)

### 5. 功能验证

部署后测试新功能：

1. **前端验证**
   - 访问 https://tt.xjj.se
   - 进入模型管理页面
   - 查看模型价格卡片，确认显示"上下文: XXK | 输出: XXK"

2. **后端验证**
   - 发送一个超过模型上下文限制的请求
   - 预期返回: `{"error": "context_length_exceeded", ...}`

```bash
# 测试上下文限制（假设某模型限制为 128K tokens）
curl -X POST http://127.0.0.1:8088/api/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -d '{
    "model": "gpt-3.5-turbo",
    "messages": [{"role": "user", "content": "'$(python3 -c "print('x' * 500000)")'"]}]
  }'
```

## 🔄 回滚步骤

如果部署后发现问题，可以快速回滚：

```bash
# 停止服务
systemctl stop switchyard.service

# 找到最新的备份
ls -lt /var/backups/switchyard/ | head -5

# 恢复备份（替换 TIMESTAMP）
TIMESTAMP=20261021-143000  # 从上面的列表中获取
cp /var/backups/switchyard/switchyard-${TIMESTAMP} /usr/local/bin/switchyard
cp /var/backups/switchyard/switchyard-${TIMESTAMP}.db /var/lib/switchyard/switchyard.db

# 启动服务
systemctl start switchyard.service

# 验证
systemctl status switchyard.service
curl http://127.0.0.1:8088/healthz | jq .
```

## 📝 部署日志

记录部署信息：

- **部署时间**: $(date)
- **操作人**: $(whoami)
- **版本**: v0.40.0
- **提交**: 70135ba
- **变更内容**: 
  - 前端：模型价格卡片展示上下文窗口和最大输出
  - 后端：relay 层添加上下文限制检查
  - 测试：添加上下文限制单元测试

## 🆘 故障排查

### 服务启动失败

```bash
# 查看详细错误
journalctl -u switchyard.service -n 50 --no-pager

# 检查二进制文件
ls -lh /usr/local/bin/switchyard
file /usr/local/bin/switchyard

# 手动启动查看错误
/usr/local/bin/switchyard
```

### 健康检查失败

```bash
# 检查端口监听
netstat -tlnp | grep 8088
ss -tlnp | grep 8088

# 检查数据库
sqlite3 /var/lib/switchyard/switchyard.db ".tables"

# 查看配置
cat /etc/switchyard/config.yaml
```

### 性能问题

```bash
# 查看资源使用
top -p $(pidof switchyard)
ps aux | grep switchyard

# 查看连接数
netstat -an | grep 8088 | wc -l
```

## 📞 联系方式

如有问题，查看：
- 项目仓库: /srv/deepseek-harness/workspaces/octopus
- 部署文档: DEPLOYMENT_SUMMARY.md
- 日志: journalctl -u switchyard.service -f
