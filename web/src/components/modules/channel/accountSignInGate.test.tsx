import { describe, expect, it } from 'vitest';
import { renderSSR } from '@/test/ssr';
import { TooltipProvider } from '@/components/ui/tooltip';
import { AccountSignIn } from './AccountSignIn';
import { accountPlatformMismatch, defaultAccountPlatform } from './accountPlatform';
import type { AccountPlatform, Dialect } from '@/api/channel';
import en from '@/locales/en.json';
import zhHans from '@/locales/zh_hans.json';
import zhHant from '@/locales/zh_hant.json';

// AccountSignIn 是「粘贴 JSON」之外的第二条导入路径, 它同样把凭据挂到某个渠道上,
// 所以平台选错的后果完全相同: relay 的 ProtocolGeminiCLI 分支要求 platform == antigravity,
// 否则每请求必失败并被硬熔断 30 分钟。一条路径拦、另一条放, 等于没拦。
//
// 但它比粘贴路径更贵: 用户要先跑完整个浏览器授权流程。授权成功、凭据却用不了,
// 会让人以为是授权本身有问题, 于是反复重试。故断言分两层: 预选值合法, 且面板会先说清。

const signIn = (dialect?: Dialect) => (
    <TooltipProvider>
        <AccountSignIn channelId={1} dialect={dialect} />
    </TooltipProvider>
);

describe('AccountSignIn 的平台预选与方言校验', () => {
    // 面板是折叠的, 展开与否在 SSR 里都是同一个初值; 这里直接断言"预选值"这条契约 ——
    // 它由 defaultAccountPlatform 决定, 是纯函数, 不需要展开面板就能钉住。
    it('订阅 / gemini_cli 渠道预选 antigravity', () => {
        expect(defaultAccountPlatform('subscription')).toBe('antigravity');
        expect(defaultAccountPlatform('gemini_cli')).toBe('antigravity');
    });

    it('预选值本身必须合法(否则面板一打开就自相矛盾)', () => {
        const cases: Array<Dialect | undefined> = [undefined, 'generic', 'xunfei', 'gemini_cli', 'subscription'];
        for (const dialect of cases) {
            expect(accountPlatformMismatch(dialect, defaultAccountPlatform(dialect))).toBeUndefined();
        }
    });

    // 授权登录支持 claude / codex / gemini / antigravity 四个平台。订阅方言下其余三个都被挡,
    // 只留 antigravity —— 这一条把"两条导入路径用同一套规则"钉住。
    it('订阅方言下 claude / codex / gemini 都被判为不合法', () => {
        const supported: AccountPlatform[] = ['claude', 'codex', 'gemini', 'antigravity'];
        for (const platform of supported) {
            const mismatch = accountPlatformMismatch('subscription', platform);
            if (platform === 'antigravity') {
                expect(mismatch).toBeUndefined();
            } else {
                expect(mismatch, `订阅方言不应放行 ${platform}`).toEqual({ required: 'antigravity', selected: platform });
            }
        }
    });

    it('SSR 渲染出折叠入口, 不因校验逻辑而崩', () => {
        // 折叠态就渲染一个 trigger; 这条同时保证"新加的校验没有在渲染期抛错"。
        const html = renderSSR(signIn('subscription'));
        expect(html).toContain(en.channel.account.signIn.trigger);
    });

    it('独立导入(方言未知)不预选受限平台', () => {
        // 独立导入的候选渠道来自渠道列表, 那份 DTO 没有 dialect; 此时不猜, 走历史默认。
        expect(defaultAccountPlatform(undefined)).toBe('codex');
    });

    // 文案共用: 两条导入路径印的是同一句话 —— 用户看到的是同一个系统,
    // 换条路径换个说法会让人以为是两种问题。
    // 三语的键集同构由 locales.test.ts 钉死, 但那只保证"键在", 不保证占位符在;
    // 少了 {selected}, 三语会各自印出一句只说半截原因的话。
    for (const [name, dict] of [['en', en], ['zh_hans', zhHans], ['zh_hant', zhHant]] as const) {
        it(`${name} 的 platformNotAllowed 带齐两个占位符`, () => {
            const copy: string = dict.channel.account.platformNotAllowed;
            expect(copy).toContain('{required}');
            expect(copy).toContain('{selected}');
        });
    }
});
