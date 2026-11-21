import { describe, expect, it } from 'vitest';
import { ACCOUNT_PLATFORMS, accountPlatformMismatch, defaultAccountPlatform } from './accountPlatform';
import { validateAccountJSON } from './AccountImport';
import type { AccountPlatform } from '@/api/channel';
import en from '@/locales/en.json';
import zhHans from '@/locales/zh_hans.json';
import zhHant from '@/locales/zh_hant.json';

// 这一组测的是「凭据平台」与「渠道方言」的对应关系。
//
// 它守的不是一个界面细节, 而是一条会造成整条渠道全失败的路径:
// 后端 op.AccountImport 完全不看渠道方言, 任何平台都能挂进任何渠道, 于是贴错平台在导入这一步**永远成功**;
// 代价在转发期才暴露 —— internal/relay/channel.go 的 ProtocolGeminiCLI 分支要求
// account_platform == antigravity, 否则直接报错, 错误落到凭据级硬熔断, 这把凭据被隔离 30 分钟。
// 使用者看到的是"渠道里有账号、也有模型, 但每个请求都失败", 而原因在转发日志里。
//
// 所以断言分两层:
// 1) 平台清单必须收全(少一个平台, 那类凭据就只能挂到别的平台名下, 正是上面那条路径的入口);
// 2) 前检对"订阅/gemini_cli 方言"必须拦下非 antigravity, 对其它方言必须放行 antigravity。

describe('ACCOUNT_PLATFORMS', () => {
    it('包含后端解析分支支持的全部平台', () => {
        // 来源: internal/model/account_json.go 的 AccountJSONParse 分支
        // (codex / kiro / gemini / grok / claude / antigravity)。
        // 少一个的后果不是"少一个按钮", 而是那类凭据只能挂到别的平台名下 —— 导入成功, 转发全 403。
        expect(ACCOUNT_PLATFORMS).toEqual(['codex', 'kiro', 'gemini', 'grok', 'claude', 'antigravity']);
    });

    it('每个平台都有本地预检分支', () => {
        // validateAccountJSON 的 switch 缺分支时返回空数组, 也就是"任何 JSON 都通过" ——
        // 预检形同虚设, 而且看不出来。用一份必然缺字段的空对象把每个平台都过一遍。
        for (const platform of ACCOUNT_PLATFORMS) {
            const errors = validateAccountJSON(platform, '{}');
            expect(errors.length, `${platform} 没有预检分支`).toBeGreaterThan(0);
        }
    });

    it('清单里没有重复项', () => {
        expect(new Set(ACCOUNT_PLATFORMS).size).toBe(ACCOUNT_PLATFORMS.length);
    });

    // 弹层对每个平台都印一句 rawHint.antigravity 式的说明(见 AccountImport 的 t(`rawHint.${platform}`))。
    // 缺键时 use-intl 不会报错, 而是把 "channel.account.rawHint.antigravity" 这串路径直接印在界面上 ——
    // 比空白更糟: 看着像渲染 bug, 又看不出该说什么。故三语逐平台都钉一遍。
    // locales.test.ts 只保证三语键集一致(缺一个就三语一起缺), 挡不住这种"三语同时缺"。
    const LOCALES = [['en', en], ['zh_hans', zhHans], ['zh_hant', zhHant]] as const;
    for (const [name, dict] of LOCALES) {
        it(`${name} 为每个平台都准备了 rawHint`, () => {
            const hints = dict.channel.account.rawHint as Record<string, string>;
            for (const platform of ACCOUNT_PLATFORMS) {
                expect(hints[platform], `${name} 缺少 rawHint.${platform}`).toBeTruthy();
            }
            // 反过来也查一次: 多余的 hint 是没人会读的死键, 而它会让"清单是否完整"看起来比实际更全。
            for (const key of Object.keys(hints)) {
                expect(ACCOUNT_PLATFORMS).toContain(key as AccountPlatform);
            }
        });
    }

    // antigravity 的 hint 必须点出"与 Gemini CLI 的凭据不通用"这件事:
    // 只说"需要 access_token + refresh_token"的话, 手上正好有 gemini 的 oauth_creds.json
    // 的人会以为那就是要的, 贴上去导入成功, 然后每次请求 403 并熔断 30 分钟。
    it('antigravity 的 rawHint 说明了与 Gemini CLI 凭据不通用', () => {
        expect(en.channel.account.rawHint.antigravity.toLowerCase()).toContain('not interchangeable');
        expect(zhHans.channel.account.rawHint.antigravity).toContain('并不通用');
    });
});

// 订阅与 gemini_cli 方言走的都是 Antigravity 数据面(internal/subplatform 里唯一的平台
// gemini 也映射到 account_platform=antigravity), 因此只收 antigravity 凭据。
// 提到模块级: 下面两处 describe 都要用它, 其中一处还用它验证"预选值本身合法"。
const ANTIGRAVITY_ONLY: Array<'subscription' | 'gemini_cli'> = ['subscription', 'gemini_cli'];

describe('accountPlatformMismatch', () => {
    for (const dialect of ANTIGRAVITY_ONLY) {
        it(`${dialect} 方言只收 antigravity, 其余平台全部报缺`, () => {
            expect(accountPlatformMismatch(dialect, 'antigravity')).toBeUndefined();
            const others = ACCOUNT_PLATFORMS.filter((p) => p !== 'antigravity');
            expect(others.length).toBeGreaterThan(0);
            for (const platform of others) {
                const mismatch = accountPlatformMismatch(dialect, platform);
                expect(mismatch, `${dialect} 方言不应放行 ${platform}`).toBeDefined();
                expect(mismatch!.required).toBe('antigravity');
                expect(mismatch!.selected).toBe(platform);
            }
        });
    }

    // 反方向**不挡**: buildOutbound 的 ProtocolGeminiCLI 分支只认 platform, 不看 dialect。
    // 一个 generic 渠道只要某条授权带着 GeminiCLI 协议位, 用 antigravity 凭据是能转发出去的。
    // 把能用的组合拦在门外, 与漏挡一样是缺陷, 只是方向相反。
    it('generic / xunfei 方言放行 antigravity', () => {
        expect(accountPlatformMismatch('generic', 'antigravity')).toBeUndefined();
        expect(accountPlatformMismatch('xunfei', 'antigravity')).toBeUndefined();
    });

    it('generic / xunfei 方言放行任意平台', () => {
        for (const platform of ACCOUNT_PLATFORMS) {
            expect(accountPlatformMismatch('generic', platform)).toBeUndefined();
        }
    });

    // 独立导入入口(渠道列表页)拿不到目标渠道的方言: 那边只有 channel_id 与名称,
    // 后端 model.ChannelStats 不带 dialect/sub_platform。此时不猜 —— 猜错的代价是
    // 把正确操作拦下来, 而漏放的代价只是回到"由后端在转发期报错"的现状。
    it('方言未知(独立导入)时一律放行', () => {
        for (const platform of ACCOUNT_PLATFORMS) {
            expect(accountPlatformMismatch(undefined, platform)).toBeUndefined();
        }
    });
});

describe('defaultAccountPlatform', () => {
    // 预选必须与"唯一合法平台"一致: 否则弹层一打开就顶着一行红字,
    // 而使用者还没做任何操作 —— 那条提示该出现的时候是他选错了平台。
    for (const dialect of ANTIGRAVITY_ONLY) {
        it(`${dialect} 方言预选 antigravity`, () => {
            expect(defaultAccountPlatform(dialect)).toBe('antigravity');
            // 预选值本身必须通过同一套校验, 否则界面一打开就是自相矛盾的。
            expect(accountPlatformMismatch(dialect, defaultAccountPlatform(dialect))).toBeUndefined();
        });
    }

    it('其余方言与独立导入预选 codex(历史默认)', () => {
        expect(defaultAccountPlatform('generic')).toBe('codex');
        expect(defaultAccountPlatform('xunfei')).toBe('codex');
        expect(defaultAccountPlatform(undefined)).toBe('codex');
    });

    it('预选值永远在平台清单里', () => {
        const dialects = ['generic', 'xunfei', 'gemini_cli', 'subscription', undefined] as const;
        for (const dialect of dialects) {
            expect(ACCOUNT_PLATFORMS).toContain(defaultAccountPlatform(dialect));
        }
    });
});

describe('antigravity 平台的本地预检', () => {
    // 与后端 AccountJSONParse 的 AccountPlatformAntigravity 分支逐条对齐:
    //   refresh_token 为空 -> "必须提供带 refresh_token 的 OAuth 凭据"; Cookie 形态不支持
    //   有 refresh_token 但 access_token 为空 -> "提供了 refresh_token 但缺少 access_token"
    it('缺 refresh_token 时说明只能走 OAuth, 且点明不支持 Cookie', () => {
        // 这一条是纠错指路: 手上只有 gemini 的 c_ses Cookie 时, 笼统说"缺字段"会让人
        // 去补一个根本不存在的字段, 而不是去跑 Antigravity 授权流程。
        const errors = validateAccountJSON('antigravity', JSON.stringify({ c_ses: 'cookie-value' }));
        expect(errors).toHaveLength(1);
        expect(errors[0]).toContain('refresh_token');
        expect(errors[0]).toContain('Cookie');
    });

    it('有 refresh_token 但缺 access_token 时报错', () => {
        const errors = validateAccountJSON('antigravity', JSON.stringify({ refresh_token: 'rt' }));
        expect(errors).toHaveLength(1);
        expect(errors[0]).toContain('access_token');
    });

    it('access_token + refresh_token 齐全时通过', () => {
        const raw = JSON.stringify({ access_token: 'at', refresh_token: 'rt', email: 'a@b.c', expiry_date: 1900000000000 });
        expect(validateAccountJSON('antigravity', raw)).toEqual([]);
    });

    it('空对象只报一条(不是"缺两个字段")', () => {
        // 缺 refresh_token 时 access_token 也必然缺, 但那是同一个根因的两面:
        // 报两条会让使用者以为要补两个字段, 而实际只要跑一次授权。
        expect(validateAccountJSON('antigravity', '{}')).toHaveLength(1);
    });

    // 与 gemini 的对比钉在这里: 同一份 OAuth 令牌在 gemini 与 antigravity 两个平台下都通过预检,
    // 但**不能互换** —— 差别在授予的 scope(cclog / experimentsandconfigs), 不在字段形状。
    // 字段预检看不出来, 只有平台↔方言那条校验能挡(见 accountPlatformMismatch)。
    it('与 gemini 的 OAuth 令牌形状相同, 平台互换与否不在这层判断', () => {
        const raw = JSON.stringify({ access_token: 'at', refresh_token: 'rt' });
        expect(validateAccountJSON('gemini', raw)).toEqual([]);
        expect(validateAccountJSON('antigravity', raw)).toEqual([]);
    });

    it('antigravity 不接受纯 Cookie(即使字段齐全)', () => {
        expect(validateAccountJSON('antigravity', JSON.stringify({ cookies: { c_ses: 'x' }, access_token: 'at' }))
            .length).toBe(1);
    });
});

describe('平台标注', () => {
    // AccountPlatform 是编译期的联合类型, 这里只是把"清单里出现的字面量都属于该类型"
    // 钉住: 将来有人往 ACCOUNT_PLATFORMS 里塞一个自由字符串, tsc 会先在类型上报错。
    it('ACCOUNT_PLATFORMS 的元素都属于 AccountPlatform', () => {
        const typed: AccountPlatform[] = ACCOUNT_PLATFORMS;
        expect(typed).toBe(ACCOUNT_PLATFORMS);
    });
});
