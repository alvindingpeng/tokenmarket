import type { AccountPlatform, Dialect } from '@/api/channel';

// ACCOUNT_PLATFORMS 是导入弹层里的平台按钮清单。
//
// 写死在前端而不是从后端拉: 支持哪些平台由后端 model.AccountJSONParse 的解析分支决定,
// 后端没有「列出我支持哪些平台」的接口, 硬造一个只会多一份需要同步的清单。
// 但**这份清单必须与解析分支逐一对齐** —— 少一个平台, 那个平台的凭据就只能挂在别的平台名下,
// 导入成功只是厄运的开始(见 accountPlatformMismatch 的说明)。
export const ACCOUNT_PLATFORMS: AccountPlatform[] = ['codex', 'kiro', 'gemini', 'grok', 'claude', 'antigravity'];

// AccountPlatformMismatch 描述「这个渠道的方言不收这个平台的凭据」。
export type AccountPlatformMismatch = {
    // required 是该方言唯一收的平台。
    required: AccountPlatform;
    // selected 是使用者当前选的平台。
    selected: AccountPlatform;
};

// defaultAccountPlatform 给出导入弹层打开时预选的平台。
//
// 订阅与 gemini_cli 渠道只有一个合法平台, 预选它: 否则弹层一打开就顶着一行红字
// 「这个渠道只接受 antigravity」, 而使用者只是想贴一份凭据 —— 那行该出现的时候
// 是他**选错了**平台, 不是他刚打开弹层。
export function defaultAccountPlatform(dialect: Dialect | undefined): AccountPlatform {
    if (dialect === 'subscription' || dialect === 'gemini_cli') return 'antigravity';
    // 其余方言没有受限平台, 保持历史默认(codex 的凭据文件最普及, 是导出入门平台)。
    return 'codex';
}

// accountPlatformMismatch 判断凭据平台与渠道方言是否对不上。
//
// 为什么需要这道前置校验: 后端 op.AccountImport 完全不看渠道方言, 任何平台都能挂进任何渠道,
// 于是「贴错平台」在导入这一步永远成功。代价在转发时才浮现 ——
// internal/relay/channel.go 的 ProtocolGeminiCLI 分支要求 account_platform 为 antigravity
// (且 account_kind 为 oauth), 否则直接返回错误; 错误落到凭据级硬熔断, 这把凭据被隔离 30 分钟。
// 症状是「渠道里有账号、也有模型, 但每一次请求都失败」, 而报错文字在转发日志里, 不是使用者看得到的地方。
//
// 凭据为什么不能互换(internal/model/account_json.go 的 AccountPlatformAntigravity 注释):
// gemini 用 Gemini CLI 的 OAuth client(cloud-platform + userinfo.*), antigravity 用自己的 client
// 并多申请 cclog 与 experimentsandconfigs。拿 gemini 的令牌请求 Antigravity 的数据面一律 403
// "The caller does not have permission" —— 缺的正是那两个 scope。两者不是同一把钥匙。
//
// 只挡「订阅/gemini_cli 方言选了别的平台」这个方向, 反方向(普通方言选 antigravity)不挡:
// buildOutbound 的 ProtocolGeminiCLI 分支只认 platform, 不看 dialect —— 一个 generic 渠道
// 只要某条授权带着 GeminiCLI 协议位, 用 antigravity 的凭据是能转发出去的。
// 把能用的组合拦在门外, 与漏挡一样是缺陷, 只是方向相反。
export function accountPlatformMismatch(
    dialect: Dialect | undefined,
    platform: AccountPlatform,
): AccountPlatformMismatch | undefined {
    // dialect 为空表示「独立导入」入口: 目标渠道是从渠道列表页选的, 那边只有 channel_id 与名称,
    // 没有方言可判(见后端 model.ChannelStats 的字段集)。此时不猜, 放行。
    if (dialect !== 'subscription' && dialect !== 'gemini_cli') return undefined;
    // 订阅方言与 gemini_cli 方言走的都是 Antigravity 数据面(internal/subplatform 里唯一的
    // 平台 gemini 也映射到 account_platform=antigravity), 因此只收 antigravity 凭据。
    if (platform === 'antigravity') return undefined;
    return { required: 'antigravity', selected: platform };
}
