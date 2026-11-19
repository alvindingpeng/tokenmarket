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
 * claude / codex / gemini 有条目。其余两个平台刻意留空:
 * - kiro:  没有官方 CLI (npm @aws/kiro-cli 404, 只有一个个人发布的占位包)
 * - grok:  未核实到官方 CLI 及其落盘路径, 不编命令
 *
 * gemini 起初也没有条目: 当时解析层只吃 Cookie(c_ses), 而 CLI 产出的是 OAuth 令牌,
 * 贴进来只会报缺 c_ses。G1 补齐了解析与刷新能力之后它才成了有效的一条 ——
 * 也就是说这张表的每一行都对应一个"确实能用"的路径, 不是"有这个 CLI"就算数。
 *
 * 留空而不是给一条「暂无 CLI」的提示: 后者会被理解成加载失败或功能缺失,
 * 而实际是这个平台本来就没有这条路。
 */

import type { AccountPlatform } from '@/api/channel';

export interface CliImportHint {
    /** 触发登录要输入的内容(见 commandKind)。 */
    command: string;
    /**
     * command 属于哪种输入:
     *   'shell'  —— 在终端里直接执行的命令, 如 claude auth login
     *   'prompt' —— 在 CLI 交互式会话里输入的斜杠命令, 如 /auth
     * 这两者对使用者是不同的动作: 把 /auth 当 shell 命令敲会得到"command not found",
     * 而把 claude auth login 敲进会话里同样没用。界面上必须用不同的措辞。
     */
    commandKind: 'shell' | 'prompt';
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
                commandKind: 'shell',
                // 二进制原文: Read(~/.claude/.credentials.json)
                path: '~/.claude/.credentials.json',
                windowsPath: '%USERPROFILE%\\.claude\\.credentials.json',
            };
        case 'codex':
            return {
                // 二进制原文: Run codex login
                command: 'codex login',
                commandKind: 'shell',
                // 二进制原文: auth.json, 位于 CODEX_HOME(默认 ~/.codex)
                path: '~/.codex/auth.json',
                windowsPath: '%USERPROFILE%\\.codex\\auth.json',
            };
        case 'gemini':
            return {
                // Gemini CLI 没有 auth/login 子命令(发行包内只定义了 mcp、extensions 等子命令),
                // 登录是在交互式会话里输入斜杠命令 /auth。编一条 `gemini auth login` 会得到
                // command not found —— 所以这里必须是 prompt 类型, 界面上也要说清楚"在会话里输入"。
                command: '/auth',
                commandKind: 'prompt',
                // 二进制原文: (string-append (param "HOME_DIR") "/.gemini/oauth_creds.json")
                path: '~/.gemini/oauth_creds.json',
                windowsPath: '%USERPROFILE%\\.gemini\\oauth_creds.json',
            };
        default:
            return undefined;
    }
}

/** 有 CLI 引导的平台。用于测试反向断言与界面枚举。 */
export const CLI_GUIDED_PLATFORMS: AccountPlatform[] = ['claude', 'codex', 'gemini'];
