# 公开渠道状态页

2026-10-19。对外的状态页: `GET /status`(以及 `/status/`), 无需登录, 单文件内嵌 HTML
(`internal/server/handlers/status.html` + `internal/server/handlers/status_page.go`),
数据来自公开统计接口 `GET /api/v1/public/channel/stats`。目标就写在标题下:
**让用户买得明白，让渠道凭实力竞争**。

## 1. 路由

| 路径 | 方法 | 鉴权 | 说明 |
| --- | --- | --- | --- |
| /status | GET | 无 | 状态页本体 |
| /status/ | GET | 无 | 同上(尾斜杠写法直接命中, 不走 gin 的 301) |
| /api/v1/public/channel/stats | GET | 无 | 统计快照(后端另有实现) |

路由沿用 router.NewGroupRouter 约定: 分组 `/status` 下 `NewRoute("")` 即分组根 /status,
再补 `NewRoute("/")` 覆盖尾斜杠。页面不接鉴权中间件, 与 share.html 同为匿名只读页;
服务端只下发聚合观测数据, 不含渠道名称、ID、归属与密钥。

## 2. 数据契约 (API → 页面)

顶层: `generated_at`(ISO 时间或秒/毫秒时间戳)、`window_seconds`(默认 900)、
`refresh_seconds`(默认 15)、`models[]`。响应可带 `{code,message,data}` 外壳, 页面自动解包。

| 字段 | 类型 | 空值语义 | 页面展示 |
| --- | --- | --- | --- |
| channel_code | string | — | 只显示 `#编码`, 不显示任何名称 / ID / 归属 |
| model_name | string | 显示 — | 模型名(textContent 渲染, 视为不可信文本) |
| kind | string | 空串 = 文本 | 文本 / 生图 / 生视频 |
| user_price | {input,output,cache_read,cache_write} | 缺项显示 — | 对客单价, USD / 1M tokens |
| official_price | 同上, 可空 | null = 未登记参考价 | 官方参考价(USD / 1M tokens) |
| price_ratio | {input,output,cache_read,cache_write 各可空} | null = 无可比 | 比价 ×, 分母缺失或为 0 一律显示 — |
| user_media | MediaPrice(per_image / per_video / per_second / resolution) | 缺项显示 — | 媒体价, 单位见 §4 |
| status | string | 未知值按灰色 + 原文 | 绿/琥珀/红/灰 + 文字(不靠颜色单独表意) |
| attempts | number | — | 窗口内尝试次数(0 是真实值, 照实显示) |
| success_rate | number 0..1, 可空 | null = 无样本 | 百分比, null 显示 — |
| latency_ms | number, 可空 | null = 无样本 | 上游延迟毫秒, null 显示 — |
| cache_read_ratio | number 0..1, 可空 | null = 无样本 | 百分比, null 显示 — |
| history[] | {start,status,attempts,success_rate} | 空数组 = 灰条 | 15 分钟窗口色条, 每桶可聚焦 + 文字浮层 |

**空值一律渲染为 —(U+2014), 绝不用 0 充数**; 0 只在数据真是 0 时出现(如 attempts=0、价格 0)。

## 3. 统计口径(页面明示给用户)

- 数据是统计窗口内 **真实上游调用** 的观测结果, **不是主动探测 / 拨测**; 窗口内无真实流量的渠道按灰色「无样本」展示。
- 已取消 / 主动中断的请求 **不计入** 尝试次数与成功率。
- 耗时为 **真实调用实测值**(非合成拨测): 自发起上游调用到本次尝试完成(含流式返回),
  取窗口内 **成功尝试的平均耗时**(毫秒); 页面展示为「上游耗时(均值)」, 不做二次加工。

> **延迟口径(已查证结案)**: `latency_ms` = 窗口内成功尝试的 `duration_ms` 均值; `duration_ms` 由
> `attemptRow` 自 `RoundStartedAt` 计到该次尝试终态(internal/relay/persist.go), 即
> 「发起本轮上游调用 → 响应完成(含流式)」的实测耗时, 确属真实调用指标、非拨测, 但不是纯上游等待。
> 页面文案据此写成「上游耗时(均值)」。若要纯上游口径, `UpstreamTiming` 已有 DNS/Connect/TLS/TTFB
> 分解(internal/model/relaylog.go: 上游净等待 ≈ TTFB − DNS − Connect − TLS), 后端换聚合字段后
> 页面同步改文案即可。

> **口径待对齐(后端 / 船长)**: 需求与页面口径是「已取消 / 主动中断的请求不计入尝试与成功率」,
> 但 internal/op/channel_status.go 目前把 `canceled`/`client_error` 终态计为失败
> (channelStatusAttemptSucceeded 只认 `success`)。两侧需择一收敛: 要么后端剔除客户端取消样本,
> 要么本页口径文案改为「含客户端取消计失败」。收敛前本页文案按需求口径书写。
- **缓存读占比不是供应商质量评分**, 只反映流量的缓存命中情况, 页面明确写出。
- 页面只展示匿名 `#编码`, 不展示渠道名称、ID、归属人。

## 4. 价格单位

- 文本模型: USD / 1M tokens(输入 / 输出 / 缓存读 / 缓存写), 页面标注单位。
- 媒体模型(kind=image|video): 沿用 MediaPrice 三个独立计价维度 ——
  `per_image` USD/张、`per_video` USD/条(固定起步价)、`per_second` USD/秒,
  `resolution` 分档价按分辨率覆盖单价。与 token 体系不同单位, 页面单独成块。
- **官方参考价是本地登记的参考价格, 不是实时核验的官方报价**; 参考价缺失或分母为 0 时比价显示 N/A(—),
  **媒体模型未登记参考价时不宣称任何官方比价**。

## 5. 前端行为

- 语言: 简体中文默认, 可切繁體中文 / English(localStorage 记忆, `<html lang>` 同步)。
- 主题: 跟随系统深浅色, 一键三态切换(系统 / 浅色 / 深色), 无外部字体与 CDN 脚本。
- 轮询: 每 `refresh_seconds`(默认 15s)拉取一次, **不重叠**(上一轮未结束直接跳过, 不排队堆积);
  页面隐藏时跳过轮询、重新可见立即补拉; 每次请求带 AbortController 10s 超时中断。
- 失败降级: 保留最近一次成功快照, 顶部 role=alert 横幅显示「正在显示 X 时刻的快照」+ 重试按钮;
  超过 2 个刷新周期未更新显示过期提示; 全部失败且无快照时显示空态。
- 检索 / 过滤 / 排序: 按模型名或 #渠道编码搜索, 按状态过滤(正常 / 降级 / 异常 / 无样本),
  按成功率 / 延迟 / 输入价排序(空值排末位, 不按 0 参与排序); 卡片 / 表格两种视图, 响应式布局。
- 每模型一条 15 分钟色条: 每个时间桶可键盘聚焦, 浮层给出时间区间、状态、尝试次数、成功率文字。
- 安全: 全部 DOM 用 createElement + textContent 构造, 无 innerHTML 注入面;
  只渲染白名单字段, 接口多给的名称 / ID / 归属一律不渲染(见 web/e2e/public-status.spec.ts 的 XSS 用例)。

## 6. 回归防线

- `web/e2e/public-status.spec.ts` + `web/playwright.public.config.ts`: 免登录直接喂内嵌 HTML 与 mock JSON,
  覆盖 360/390/1280 三档宽度不横向溢出、#编码展示、XSS 文本化、搜索过滤、503 横幅 + 重试恢复。
- `scripts/check.sh` 的「公开状态页 e2e」一步即跑该套件。

## 7. 稳定测试钩子(供 e2e, 均为对外稳定约定)

| 钩子 | 元素 | 说明 |
| --- | --- | --- |
| `#search` / `[data-testid="search"]` | 搜索输入 | input 事件即过滤(模型名或 #编码) |
| `#status-filter` / `[data-testid="status-filter"]` | 状态下拉 | all / ok / warn / bad / none |
| `#sort` / `[data-testid="sort"]` | 排序下拉 | default / success / latency / price |
| `#refresh` / `[data-testid="refresh"]` | 刷新按钮 | 手动取数, 失败后重试也用它 |
| `#models` / `[data-testid="models-list"]` | 列表容器 | 卡片网格 / 表格 / 空态都挂在它下面(role=region) |
| `[data-testid="model-card"]` | 每个模型卡片 | 卡片视图 |
| `[data-testid="model-row"]` | 每个模型行 | 表格视图 |
| `#theme` / `[data-testid="theme"]` | 主题切换 | aria-label 默认「切换主题」, 随语言同步 |
| `#langZh` / `#langHant` / `#langEn` | 语言切换 | 可见文本 简体中文 / 繁體中文 / English |
| `.bucket` | 色条时间桶 | 每桶 aria-label 含时间区间、状态、尝试、成功率 |

可见文案默认中文(状态: 正常 / 降级 / 异常 / 无样本; 空态: 暂无公开数据;
快照横幅含「最近成功快照」; 价格块: 对客单价（USD / 1M tokens）等), 可直接断言;
搜索框、状态/排序下拉、视图切换组、列表区域均有 aria-label(默认中文, data-i18n-aria 随语言同步)。
HTML 可直接从磁盘经 page.route(document) 喂给浏览器做 fixture 测试(见 web/e2e/public-status.spec.ts)。


## 最终上线口径（以此节为准）

- 上游首字节延迟为成功请求尝试的平均 HTTP TTFB，来自 `ttfb_ns`，不含客户端流式写入总时长，不等同首 token TTFT；没有正数计时样本返回 null。
- 排除在途、取消、客户端错误、策略拦截、模型不匹配与本地限流；success 尝试必须与最终请求 success 对齐，避免首帧提前成功。
- 0 样本为 no_data；1–4 为 low_sample；至少 5 次时 ≥99% 正常，50%–99% 降级，低于50%异常。当前模型熔断与已停用覆盖观测状态。
- 仍发布的停用模型保持可见；撤回发布、下架和撤销信任会从缓存返回前重新过滤。
- 缓存读取 token 超过总输入等异常比例返回 null，不伪造100%。媒体模型没有文本官方比价。
