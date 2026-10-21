# 模型上下文限制功能实现总结

## 📋 功能概述

为 Switchyard 价格页添加模型上下文窗口信息展示，并在 relay 层调用渠道模型时检查请求是否超过模型上下文限制。

## ✅ 已完成

### 1. 前端展示 (web/)

#### 文件修改：
- **web/src/api/model.ts**
  - 在 `LLMPrice` 接口添加 `context` 和 `max_output` 字段
  - 类型定义与后端 ModelCapability 对齐

- **web/src/components/modules/model/Item.tsx**
  - 模型价格卡片底部添加上下文窗口显示
  - 格式：`上下文: 128K | 输出: 16K`
  - 未注册时显示 "未注册"

- **web/src/components/modules/model/index.tsx**
  - 在价格列表页添加表格列展示

#### 国际化文案：
- **zh_hans.json**: "上下文", "最大输出", "未注册"
- **zh_hant.json**: "上下文", "最大輸出", "未註冊"  
- **en.json**: "Context", "Max Output", "Not Registered"

### 2. 后端上下文检查 (internal/relay/)

#### 新增文件：
- **internal/relay/handler_context_test.go**
  - 测试上下文限制检查逻辑
  - 测试边界条件（超限、未注册、正常）

#### 修改文件：
- **internal/relay/handler.go**
  - 添加 `checkContextLimit()` 函数
  - 在请求转发前（第 325 行附近）调用检查
  - 超限时返回 `context_length_exceeded` 错误（已有错误类型）
  - Context=0 视为未注册，跳过检查

#### 检查逻辑：
```go
1. 根据 modelName 查询 ModelCapability.Context
2. 若 Context > 0 且已注册：
   - 估算请求 token 数 (estimateTokens)
   - 若 estimatedTokens > Context，返回错误
3. 若 Context = 0（未注册），跳过检查
```

## 📦 构建与部署

### 本地构建
```bash
bash scripts/build-local.sh
# 产物: build/switchyard-local (52MB)
# 版本: v0.40.0 (commit 70135ba)
```

### 部署脚本

1. **scripts/deploy-manual.sh** ✅ 推荐
   - 半自动：上传二进制后提供详细的服务器命令
   - 需要手动 SSH 执行部署步骤
   - 无需额外依赖

2. **scripts/deploy-auto.sh**
   - 全自动但需要 sshpass（容器环境无法安装）
   - 通过环境变量 `DEPLOY_PASSWORD` 传递密码

### 部署目标
- 服务器: tt.xjj.se
- 用户: root
- 服务: switchyard.service
- 二进制路径: /usr/local/bin/switchyard

## 🧪 测试结果

```
✅ internal/relay 包编译通过
✅ internal/relay 测试通过（包括新增的上下文限制测试）
✅ 前端构建成功（14.2s）
✅ 后端构建成功（v0.40.0, commit 70135ba）
```

## 📝 Git 提交

```
commit 70135ba
feat: 模型上下文限制展示与调用检查

前端:
- 在模型价格卡片中展示上下文窗口和最大输出
- 添加对应的本地化文案(中英繁)

后端:
- 在 relay 层添加上下文限制检查
- 请求转发前检查估算token数是否超过模型上下文限制
- 超限时返回 context_length_exceeded 错误
- 添加相关测试用例

8 files changed, 208 insertions(+), 5 deletions(-)
```

## 🔄 数据流

### 能力数据来源
1. **models.dev 价格同步** → `internal/price/price.go:collectModelCapabilities()`
2. 提取 `Limit.Context` 和 `Limit.Output`
3. 保存到 `ModelCapability` 表（Source=models.dev）
4. 手动录入能力保留（Source=manual，不会被覆盖）

### 请求检查流程
```
用户请求 
  → relay/handler.go 
  → 选择 channelModel
  → checkContextLimit(modelName, requestBody)
  → 若超限，返回 400 + context_length_exceeded
  → 否则，继续转发上游
```

## 📊 影响范围

### 前端
- 模型列表页增加上下文列展示
- 价格卡片底部增加能力信息行
- 3 个语言文件各增 3 条文案

### 后端
- relay 包新增 1 个检查函数 + 1 个测试文件
- handler.go 调用点 1 处（第 325 行附近）
- 不影响现有估算逻辑（estimateTokens 仍用于速率限制）

## 🎯 后续建议

1. **精准 token 计数**
   - 当前使用 `estimateTokens`（字符数粗估）
   - 可集成 tiktoken 或模型专用 tokenizer 提高精度

2. **前端错误提示**
   - 当用户请求超限时，前端显示友好提示
   - 建议用户减少输入或切换更大上下文模型

3. **能力管理 UI**
   - 添加能力管理页面
   - 支持手动编辑未同步模型的能力

4. **监控与告警**
   - 统计超限请求频率
   - 分析哪些模型/用户经常触发限制

## 🚀 下一步

执行以下命令完成部署：

```bash
# 方式 1: 半自动部署（推荐）
bash scripts/deploy-manual.sh

# 方式 2: 如果服务器上已安装 sshpass
DEPLOY_PASSWORD='kLaRns0425' bash scripts/deploy-auto.sh
```

部署后验证：
```bash
# 检查版本
ssh root@tt.xjj.se "/usr/local/bin/switchyard version"

# 检查健康状态
ssh root@tt.xjj.se "curl -s http://127.0.0.1:8088/healthz | jq ."

# 查看服务日志
ssh root@tt.xjj.se "journalctl -u switchyard.service -f"
```
