import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { CLI_GUIDED_PLATFORMS, cliImportHint } from './cliImportHint';
import type { AccountPlatform } from '@/api/channel';

// 这张引导表的价值全在「准不准」: 路径写错, 使用者会去一个不存在的文件;
// 命令写错(例如只写 claude 而不是 claude auth login), 他跑起来也看不到登录。
// 所以每条都要钉死, 且**来源要留在测试里**, 将来别人改的时候知道为什么是这个值。

const ALL: AccountPlatform[] = ['codex', 'kiro', 'gemini', 'grok', 'claude'];

describe('cliImportHint', () => {
    it('claude 给出官方命令与凭据路径', () => {
        const hint = cliImportHint('claude');
        expect(hint).toBeDefined();
        // 来源: Claude Code 2.1.325 二进制原文
        //   'Run `claude auth login` (or /login in a local session)'
        //   'Read(~/.claude/.credentials.json)'
        // 只写 'claude' 会进交互式会话而不触发登录, 这条必须精确。
        expect(hint!.command).toBe('claude auth login');
        expect(hint!.path).toBe('~/.claude/.credentials.json');
    });

    it('codex 给出官方命令与凭据路径', () => {
        const hint = cliImportHint('codex');
        expect(hint).toBeDefined();
        // 来源: Codex CLI 0.169.0 二进制原文 'Run codex login'; 凭据 auth.json 位于 CODEX_HOME。
        expect(hint!.command).toBe('codex login');
        expect(hint!.path).toBe('~/.codex/auth.json');
    });

    // 反向断言: 这三个平台没有官方 CLI, 或者官方 CLI 产出的形状我们吃不下。
    // 给它们编一条命令比不给更糟 —— 使用者会照着跑然后失败, 且不知道是自己的问题。
    it('kiro 没有引导(无官方 CLI)', () => {
        expect(cliImportHint('kiro')).toBeUndefined();
    });

    // gemini 原先在这里(解析层只吃 Cookie)。G1 补齐 OAuth 解析与刷新后它成了有效路径,
    // 于是断言反过来: 必须有引导。这条变化本身就是 G1 的验收点之一。
    it('gemini 给出 /auth 会话命令与 oauth_creds.json 路径', () => {
        const hint = cliImportHint('gemini');
        expect(hint).toBeDefined();
        // Gemini CLI 没有 auth/login 子命令, 登录是在交互式会话里输入 /auth。
        // 编一条 \`gemini auth login\` 会 command not found —— 所以类型必须是 prompt。
        expect(hint!.command).toBe('/auth');
        expect(hint!.commandKind).toBe('prompt');
        // 二进制原文: (string-append (param "HOME_DIR") "/.gemini/oauth_creds.json")
        expect(hint!.path).toBe('~/.gemini/oauth_creds.json');
    });

    it('grok 没有引导(未核实官方 CLI)', () => {
        expect(cliImportHint('grok')).toBeUndefined();
    });

    it('CLI_GUIDED_PLATFORMS 与实际返回一致', () => {
        // 两份清单漂移的后果是界面显示了引导、但数据层没有 —— 或反之。
        const actual = ALL.filter((p) => cliImportHint(p) !== undefined).sort();
        expect(actual).toEqual([...CLI_GUIDED_PLATFORMS].sort());
    });

    it('claude/codex 的命令是 shell 命令, gemini 的是会话内命令', () => {
        // 这个区分必须逐平台钉住: 两者的输入位置不同, 界面上用的是不同措辞。
        expect(cliImportHint('claude')!.commandKind).toBe('shell');
        expect(cliImportHint('codex')!.commandKind).toBe('shell');
        expect(cliImportHint('gemini')!.commandKind).toBe('prompt');
    });

    it('每个有引导的平台都带 Windows 路径', () => {
        for (const platform of CLI_GUIDED_PLATFORMS) {
            const hint = cliImportHint(platform);
            expect(hint!.windowsPath).toBeTruthy();
            expect(hint!.windowsPath).toContain('%USERPROFILE%');
        }
    });
});

describe('cliHint 三语文案', () => {
    // 缺 key 会在界面上印出原始 key(本项目此前踩过这个坑)。
    const LOCALES = ['en', 'zh_hans', 'zh_hant'] as const;
    const REQUIRED = [
        'title',
        'runOnYourMachine',
        'openFile',
        'windowsPath',
        'pasteContent',
        'secretNote',
        'copy',
        'copyFailed',
    ];

    for (const locale of LOCALES) {
        it(`${locale} 的 cliHint 文案齐全`, () => {
            const raw = readFileSync(join(__dirname, '../../../locales', `${locale}.json`), 'utf-8');
            const dict = JSON.parse(raw);
            const block = dict.channel.account.cliHint;
            expect(block).toBeDefined();
            for (const key of REQUIRED) {
                expect(block[key], `${locale} 缺少 ${key}`).toBeTruthy();
            }
        });
    }

    it('英文文案不含中日韩字符', () => {
        // 英文包混入 CJK 是复制中文时最容易留下的痕迹, 只在英文界面才看得出来。
        const raw = readFileSync(join(__dirname, '../../../locales/en.json'), 'utf-8');
        const dict = JSON.parse(raw);
        const block = dict.channel.account.cliHint;
        for (const [key, value] of Object.entries(block)) {
            expect(String(value), `en 的 ${key} 含 CJK`).not.toMatch(/[\u4e00-\u9fff]/);
        }
    });
});
