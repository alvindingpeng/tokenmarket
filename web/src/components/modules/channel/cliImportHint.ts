/**
 * CLI 取凭据的导入引导数据。
 *
 * 为什么单独一个文件而不是塞进组件: 这张表是「我们对各平台官方 CLI 的已知事实」,
 * 它需要被单元测试逐条钉住 —— 路径写错会直接误导使用者去一个不存在的文件,
 * 而这种错误在界面上看不出来(界面只是如实渲染了一张错的表)。
 *
 * 每条的来源都在 docs/design-cli-account-import-guidance.md 里标了证据等级,
 * 全部取自官方发布物的二进制字符串, 不是二手文档。
 *
 * 只有 claude / codex 有条目。其余三个平台刻意留空:
 * - kiro:   没有官方 CLI (npm @aws/kiro-cli 404, 只有一个个人发布的占位包)
 * - gemini: 官方 CLI 产出的是 OAuth 令牌(access_token/refresh_token/expiry_date),
 *           而我们 gemini 解析分支吃的是 Cookie(c_ses) —— 形状不兼容, 贴进来只会报缺 c_ses;
 *           这是解析能力缺口, 不是文案能解决的(见设计文档 4 节的 G1/G2 取舍, 本轮取 G2)
 * - grok:   未核实到官方 CLI 及其落盘路径, 不编命令
 *
 * 留空而不是给一条「暂无 CLI」的提示: 后者会被理解成加载失败或功能缺失,
 * 而实际是这个平台本来就没有这条路。
 */

import type { AccountPlatform } from '@/api/channel';

export interface CliImportHint {
    /** 登录命令, 例如 claude auth login。 */
    command: string;
    /** 凭据文件的 POSIX 路径。 */
    path: string;
    /** Windows 路径。按 USERPROFILE 约定推得, 未在 Windows 实测(见设计文档第 8 节)。 */
    windowsPath: string;
}

/**
 * cliImportHint 返回某平台的 CLI 引导; 无引导时返回 undefined。
 *
 * 返回 undefined 而不是空对象: 调用方用「有没有」来分支, 空对象会让
 * if (hint) 成立却渲染出一块空面板。
 */
export function cliImportHint(platform: AccountPlatform): CliImportHint | undefined {
    switch (platform) {
        case 'claude':
            return {
                // 二进制原文: Run `claude auth login` (or /login in a local session)
                // 写 `claude` 只会进交互式会话, 不触发登录 —— 这条必须精确。
                command: 'claude auth login',
                // 二进制原文: Read(~/.claude/.credentials.json)
                path: '~/.claude/.credentials.json',
                windowsPath: '%USERPROFILE%\\.claude\\.credentials.json',
            };
        case 'codex':
            return {
                // 二进制原文: Run codex login
                command: 'codex login',
                // 二进制原文: auth.json, 位于 CODEX_HOME(默认 ~/.codex)
                path: '~/.codex/auth.json',
                windowsPath: '%USERPROFILE%\\.codex\\auth.json',
            };
        default:
            return undefined;
    }
}

/** 有 CLI 引导的平台。用于测试反向断言与界面枚举。 */
export const CLI_GUIDED_PLATFORMS: AccountPlatform[] = ['claude', 'codex'];
