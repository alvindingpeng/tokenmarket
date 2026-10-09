# 系统信息配置的分发与消费

2026-10-09 (v0.15.0)。修的是「管理员设置页面修改系统信息配置未生效」: 写入端一直是好的
(SettingSetString 落库成功), 缺的是**没有任何地方读这些值** —— 保存后自然什么都看不到。

## 1. 唯一的下发端点

GET /api/v1/site/config (internal/server/handlers/site.go), 无需登录。

- **单开一个不带 Auth 的路由分组**, 而不是往 /api/v1/setting 上加公开路由: 后者整组是管理员权限,
  把它放开等于把全部设置(含密钥)泄露出去。这里只白名单读站点相关的 7 个键。
- 下发 6 个字段: site_name / site_description / site_contact / announcement /
  maintenance_mode / maintenance_notice。
- announcement **在后端按 site_announcement_enabled 过滤**: 开关关闭时下发空串。
  这样前端只需判断「正文有没有」, 不必各自再读一次开关, 少一处能写错的地方。
- maintenance_notice 为空时后端回退 "service under maintenance", 与 login() 里拒绝请求时
  用的那句保持一致 —— 用户在登录接口收到的和被展示的必须是同一句话。

## 2. register-config 瘦身

/api/v1/user/register-config 从前重复下发 site_name / site_description / site_contact, 与站点
配置端点是同一份数据的两个来源, 迟早漂移。现在它只回答「注册开关」三件事, 并补发此前漏掉的
approval_required —— 前端 login/index.tsx 一直在读它, 后端没发, 于是开启审批后新注册用户
仍看到「注册成功, 请登录」而不是「等待管理员审批」。

## 3. 消费点

| 字段 | 消费位置 |
| --- | --- |
| site_name | 浏览器标签页标题 (web/src/lib/site-title.ts, 挂在 AppContainer 最外层, 所以登录页也生效)、登录页 h1、密钥视图抬头、首页分享图抬头 |
| site_description | 登录页标题下一行 |
| site_contact | 登录页, 自动识别 http(s) 与邮箱并渲染成链接, 纯文本原样显示 |
| announcement | SiteBanners 公告横幅(应用外壳 + 密钥视图; 登录页不展示 —— 管理员说明写的是「向全部登录用户展示」) |
| maintenance_mode + maintenance_notice | SiteBanners 红色常驻横幅(含登录页: 被维护模式挡住的人需要知道原因), 与后端 login() 的 503 同源 |

留空的 site_name 在前端回退常量 DEFAULT_SITE_NAME = "Octopus", 不把标题清成空串。

## 4. 生效时机: 谁负责失效缓存

设置写入走通用 /api/v1/setting/set, 后端无从知道刚改的键会被哪个界面消费, 因此由前端广播:
useSetSetting 的 onSuccess 除 [settings, list] 外, 还失效 [site, config] 与
[user, register-config]。管理员在「系统信息配置」里改完, 标题与横幅同帧更新, 无需刷新页面。

## 5. 版式陷阱

AppShell 的 md 网格从 [auto_minmax(0,1fr)] 改为 [auto_auto_minmax(0,1fr)], 中间那行是横幅槽,
导航容器相应改为 md:row-span-3。槽位**始终存在于 DOM**(内容为空时高度为 0): 若把整行写成
条件渲染, 出现与消失会让网格少一项, main 被挤到 auto 行上丢掉 1fr 高度 —— 整页滚动区域塌陷。

## 6. 验证

在临时实例上覆盖: 端点无需鉴权即可读取、六个字段写入后立刻可读、公告随开关出现/消失(正文仍保留)、
维护模式下非管理员登录返回 503 且提示文案与横幅同源、管理员不受维护模式影响、
维护模式关闭后登录恢复、site_name 留空回传空串、register-config 含 approval_required 且不再重复
下发站点文案、/setting/list 对匿名请求仍然 401(公开端点没有放宽权限)。
