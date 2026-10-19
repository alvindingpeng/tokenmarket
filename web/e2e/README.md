# web/e2e — 布局回归测试

这一组用例跑在**真实浏览器**里，专治单测测不出来的一类缺陷：

> 容器在纵轴塌陷 → 内容被 `overflow-hidden` 切掉 → 外层 `scrollHeight === clientHeight` 连滚动都没有
> → 表现为「按钮看不见、也滚不到」。

已知的两个事故都属于这一族：

| 版本 | 症状 | 根因 |
|---|---|---|
| v0.39.4 | 限流页策略清单只剩 2px | 用量页签 `shrink-0` 拒绝压缩，清单 `flex-1 min-h-0` 让到底 |
| v0.39.5 | 渠道编辑弹窗保存按钮不可见 | `SHELL_HEIGHT` 上多挂了一个 `flex-1`，`flex-basis: 0` 盖过 `height` |

## 为什么判据是 elementFromPoint 而不是 boundingBox

`getBoundingClientRect()` 在被裁剪、被遮盖、`pointer-events: none` 三种情况下**都显示正常**。
只有 `document.elementFromPoint(中心点)` 命中按钮自己，才证明用户真的点得到。

## 怎么跑

需要一个已登录的 JWT（服务端只认 `auth` cookie）：

```sh
# 1) 在跑着服务的机器上签一个（需要能读 data.db）
#    参考仓库根目录的运维脚本，或用管理员账号登录后从浏览器复制 cookie

# 2) 跑
SWITCHYARD_E2E_JWT="$(cat /tmp/jwt.txt)" pnpm run test:e2e
```

可选环境变量：

| 变量 | 默认 | 说明 |
|---|---|---|
| `SWITCHYARD_E2E_JWT` | 无 | **必填**，登录后的 JWT |
| `SWITCHYARD_E2E_BASE_URL` | `http://127.0.0.1:8088` | 目标实例 |

没设 `SWITCHYARD_E2E_JWT` 时整组跳过并给出明确提示，不会静默通过。

## 注意

- 落地页与控制台是**同一份 SPA**：未登录看落地页，带 cookie 时进控制台，没有独立的 `/web/` 路径。
- 视图切换用的是 `MorphingDialog`：表单与统计卡是两个绝对定位、重叠一次的视图。
  动画期间正在淡出的那份还垫在下面，命中测试会打到它 —— 用例里切换后要等退场动画结束。
