# 前端单测(vitest)

起于 2026-10-09: 前端此前 0 个测试, 计费/金额/URL 拼装这类纯逻辑只能靠后端 e2e 侧面兜底。

## 结构

- `web/vitest.config.ts`: 与 `vite.config.ts` **分开配置**, vitest 会优先读它;
  `@` 别名必须在两个文件里各写一遍, 只改 vite.config 不影响测试解析。
- `environment: "node"`: 被测对象全是纯函数与 URL 构造, 不需要 jsdom。
- 运行: `pnpm --filter switchyard test`(或在 web 目录 `pnpm test`); `test:watch` 供本地循环。

## 初始覆盖面(4 个文件 / 24 条)

| 文件 | 测什么 |
|---|---|
| `src/lib/utils.test.ts` | `cn` 类合并; `formatCount/formatMoney/formatTime` 的 K/M/B、$、d/h/m/s 档位与 undefined 回落 |
| `src/lib/pricing.test.ts` | `markupOf` 非法值回落(不能返回 NaN); `userPriceOf` 与后端计费口径一致 |
| `src/api/stats.test.ts` | `formatStatsMetrics` 合计项 = 成功+失败 / 输入+输出 |
| `src/api/urls.test.ts` | `billingExportUrl`/`auditExportUrl`: 筛选逐个落参、空串不落参、**分页不进导出地址** |

## 认证会话隔离回归（v1.4.1）

- 使用真实 Zustand 认证 store 与共享 QueryClient 验证管理员退出、切换账号及 API Key 认证时旧缓存被清理。
- 使用可控的延迟响应验证旧请求、旧 401 与旧认证检查不会污染或登出新会话；不依赖 sleep 碰竞态。
- 覆盖利润查询的 AbortSignal，以及旧 mutation 回调对认证和缓存的影响。
- 单元测试不代表真实浏览器多账号验收；取消前端请求也不能撤销服务端已经执行的操作。

## 顺带收敛的重复实现

- `formatMoney` 此前在 `billing` 与 `share` 各抄一份, 已收敛为 `src/lib/money.ts` 的 `formatPlainMoney`。
  注意它和 `lib/utils.ts` 的 `formatMoney`(带量级单位的展示型)是**两个不同的东西**, 命名按语义区分。
- `markupOf` 从 `share/index.tsx` 抽到 `src/lib/pricing.ts`, 组件只做引用。

## 已知的真实行为(测试固化, 非 bug)

- `formatTime(undefined)` 返回 `unit: ""`(缺省回落到单位表首项), **不是** `ms`; 测试按实际行为断言。
- `formatCount/formatMoney/formatTime` 的 `undefined` 一律 `raw: 0`, 展示 `0.00`。
