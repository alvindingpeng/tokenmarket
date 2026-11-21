import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createElement } from 'react';

// 时区选项、校验、账户设置行、store 与保存接口的用例都集中在这里。
//
// 为什么合成一个文件: 本任务的写域只给了 5 个文件, 其中只有这一个测试文件。
// 分文件更好读, 但越界写文件是更硬的约束。
//
// 为什么用 vi.resetModules + 动态 import 而不是顶部静态 import:
// api/user.ts 建 store 时要读 window.localStorage, logout 要读 document;
// 静态 import 会在这些 stub 装好之前就加载模块, 于是 persist 退化成空实现,
// 断言时既拿不到 getOptions() 也无从判断"哪些字段被持久化"。
// 与 src/api/user.test.ts 的既有做法一致。
// fakeWindow 只实现本文件真正用到的接口: localStorage(persist 读写)与
// addEventListener/removeEventListener(useAuth 在挂载时注册 unauthorized 监听)。
// 少了事件三件套, SettingAccount 渲染时会直接 TypeError。
function fakeWindow() {
    return {
        localStorage: { getItem: () => null, setItem: () => {}, removeItem: () => {} },
        addEventListener: () => {},
        removeEventListener: () => {},
        dispatchEvent: () => true,
    };
}

vi.resetModules();
vi.stubGlobal('window', fakeWindow());
// 不 stub document: 本文件要 import Account.tsx, 它经 sonner 在模块加载期碰 document
// (getElementsByTagName 插样式)。login/logout 里对 document 的写都有 typeof 守卫,
// node 环境下不执行, 因此无需伪造 DOM。

const { SERVER_OPTION_VALUE, buildTimezoneOptions, detectBrowserTimezone, isValidTimezoneName, optionValueToTimezone, timezoneToOptionValue } = await import('./timezoneOptions');
const { renderSSR } = await import('@/test/ssr');
const { SettingAccount } = await import('./Account');
const { TimezoneSetting } = await import('./Timezone');
const en = (await import('@/locales/en.json')).default;
const hans = (await import('@/locales/zh_hans.json')).default;
const hant = (await import('@/locales/zh_hant.json')).default;
const { useAuthStore, useUpdateTimezone } = await import('@/api/user');

beforeEach(() => {
    vi.stubGlobal('window', fakeWindow());
    // document 只在这一步之后才 stub: 模块加载期(上面那批 import)必须是 undefined,
    // 否则 sonner 会拿它插样式并因缺 head/getElementsByTagName 直接崩。
    // apiRequest 需要它做两件事: new URL(target, document.baseURI) 与写 document.cookie。
    vi.stubGlobal('document', { baseURI: 'http://localhost/', cookie: '' });
    // 不 stub document: 本文件要 import Account.tsx, 它经 sonner 在模块加载期碰 document
// (getElementsByTagName 插样式)。login/logout 里对 document 的写都有 typeof 守卫,
// node 环境下不执行, 因此无需伪造 DOM。
});
afterEach(() => {
    // 先 logout 再做断言清理: 它把会话状态置回未登录, 后面的用例从干净状态起步。
    useAuthStore.getState().logout();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
});

describe('buildTimezoneOptions', () => {
    it('第一项恒定是跟随服务器, 与 saved 无关', () => {
        for (const saved of ['', 'Asia/Shanghai', 'America/New_York']) {
            const options = buildTimezoneOptions({ saved, browser: '' });
            expect(options[0]).toEqual({ value: SERVER_OPTION_VALUE, label: 'server' });
        }
    });

    it('三个值都不同时按 server -> browser -> custom 排列', () => {
        const options = buildTimezoneOptions({ saved: 'America/New_York', browser: 'Asia/Shanghai' });
        expect(options.map((o) => o.label)).toEqual(['server', 'browser', 'custom']);
        expect(options.map((o) => o.value)).toEqual([SERVER_OPTION_VALUE, 'Asia/Shanghai', 'America/New_York']);
    });

    it('saved 与 browser 相同时只出现一项同值项(不重复, 但不能没有)', () => {
        // 两种错法各有一半:
        // - 出两项 -> 选中态落在第一项, 用户点第二项看起来毫无反应;
        // - 一项都不出 -> 受控 Select 的 value 匹配不到任何 item, 界面显示成全空,
        //   用户存着 Asia/Shanghai 却看到"未选择", 以为设置丢了。
        // 正确做法是保留唯一的那一项(它同时代表"本机时区"与"已保存值")。
        const options = buildTimezoneOptions({ saved: 'Asia/Shanghai', browser: 'Asia/Shanghai' });
        expect(options.map((o) => o.label)).toEqual(['server', 'browser']);
        expect(options.map((o) => o.value)).toEqual([SERVER_OPTION_VALUE, 'Asia/Shanghai']);
    });

    it('从未设置过(saved 为空)时只有 server 与 browser 两项', () => {
        const options = buildTimezoneOptions({ saved: '', browser: 'Asia/Shanghai' });
        expect(options.map((o) => o.label)).toEqual(['server', 'browser']);
    });

    it('探测到的名字 Intl 不认时不出这一项(否则选中后保存必然失败)', () => {
        // 隐私浏览器/插件可能给出假时区。列表里给出一个存不进去的选项,
        // 用户唯一能得到的反馈是"保存失败", 而他并不知道错在哪。
        const options = buildTimezoneOptions({ saved: '', browser: 'Not/AZone' });
        expect(options).toEqual([{ value: SERVER_OPTION_VALUE, label: 'server' }]);
    });

    it('browser 探测为空时不产生 browser 项', () => {
        const options = buildTimezoneOptions({ saved: 'America/New_York', browser: '' });
        expect(options.map((o) => o.label)).toEqual(['server', 'custom']);
    });

    it('老账号(saved 与 browser 都为空)只有 server 一项', () => {
        const options = buildTimezoneOptions({ saved: '', browser: '' });
        expect(options).toEqual([{ value: SERVER_OPTION_VALUE, label: 'server' }]);
    });

    it('任何组合下, 已存的值都在列表里(否则受控 Select 显示为空)', () => {
        const combos = [
            { saved: '', browser: 'Asia/Shanghai' },
            { saved: 'Asia/Shanghai', browser: '' },
            { saved: 'Asia/Shanghai', browser: 'Asia/Shanghai' },
            { saved: 'America/New_York', browser: 'Asia/Shanghai' },
            { saved: 'America/New_York', browser: '' },
        ];
        for (const combo of combos) {
            const options = buildTimezoneOptions(combo);
            const expected = combo.saved === '' ? SERVER_OPTION_VALUE : combo.saved;
            expect(options.some((o) => o.value === expected)).toBe(true);
        }
    });

    it('已存值 Intl 不认、浏览器又探测到同一个名字时, 仍有一项代表它', () => {
        // 一处容易被漏掉的边界: browser 项被合法性检查挡掉, 若"已保存项"的判据只是
        // saved !== browser, 那个值就没有任何一项代表它 —— 受控 Select 匹配不到 item,
        // 格子显示为空, 用户以为自己存的时区丢了。
        const options = buildTimezoneOptions({ saved: 'Not/AZone', browser: 'Not/AZone' });
        expect(options.some((o) => o.value === 'Not/AZone')).toBe(true);
        expect(options.map((o) => o.value)).toEqual([SERVER_OPTION_VALUE, 'Not/AZone']);
    });

    it('任何组合下 value 都唯一', () => {
        const combos = [
            { saved: '', browser: '' },
            { saved: '', browser: 'Asia/Shanghai' },
            { saved: 'Asia/Shanghai', browser: '' },
            { saved: 'Asia/Shanghai', browser: 'Asia/Shanghai' },
            { saved: 'America/New_York', browser: 'Asia/Shanghai' },
        ];
        for (const combo of combos) {
            const options = buildTimezoneOptions(combo);
            expect(new Set(options.map((o) => o.value)).size).toBe(options.length);
        }
    });

    it('只有 browser / custom 项带 timezone 供 {tz} 占位符使用, server 项不带', () => {
        const options = buildTimezoneOptions({ saved: 'America/New_York', browser: 'Asia/Shanghai' });
        expect(options[0].timezone).toBeUndefined();
        expect(options[1].timezone).toBe('Asia/Shanghai');
        expect(options[2].timezone).toBe('America/New_York');
    });
});

describe('isValidTimezoneName', () => {
    it('空串合法(它表示跟随服务器, 不是时区名)', () => {
        // 若把空串判非法, 选完"跟随服务器"就存不回去了。
        expect(isValidTimezoneName('')).toBe(true);
    });

    it('真实 IANA 名合法', () => {
        expect(isValidTimezoneName('Asia/Shanghai')).toBe(true);
        expect(isValidTimezoneName('UTC')).toBe(true);
    });

    it('乱写的一串非法', () => {
        expect(isValidTimezoneName('Not/AZone')).toBe(false);
        expect(isValidTimezoneName('Asia/Shanghia')).toBe(false);
    });

    it('纯数字等明显非时区名的也非法', () => {
        expect(isValidTimezoneName('12345')).toBe(false);
    });
});

describe('detectBrowserTimezone', () => {
    it('正常情况下返回非空的 IANA 名', () => {
        // 本仓库的测试环境是 Node(带完整 ICU), 有 resolvedOptions().timeZone。
        expect(detectBrowserTimezone()).not.toBe('');
    });

    it('resolvedOptions 抛异常时回落空串, 不把异常抛给调用方', () => {
        // 设置项不能因为探测失败而白屏: 退化成只有"跟随服务器"一项仍然可用。
        vi.spyOn(Intl.DateTimeFormat.prototype, 'resolvedOptions').mockImplementation(() => {
            throw new Error('no icu');
        });
        expect(detectBrowserTimezone()).toBe('');
    });

    it('resolvedOptions 返回空值/非字符串时也回落空串', () => {
        vi.spyOn(Intl.DateTimeFormat.prototype, 'resolvedOptions').mockImplementation(
            () => ({ timeZone: undefined }) as unknown as Intl.ResolvedDateTimeFormatOptions,
        );
        expect(detectBrowserTimezone()).toBe('');
    });

    it('探测结果能直接喂给 isValidTimezoneName', () => {
        // 两项互相配套: 下拉里出现的值必须能通过校验, 否则用户选了却存不进去。
        expect(isValidTimezoneName(detectBrowserTimezone())).toBe(true);
    });
});

// valueSpanText 截出触发格里那一小段(SelectValue 的 span)的文字。
//
// 为什么要截而不是直接搜整份 HTML: 选项文案也可能出现在别处(单据/提示),
// 那样断言会在"文案恰好也出现在别的地方"时变绿 —— 而这里要钉的是**那一格里有没有字**。
function valueSpanText(html: string): string {
    const start = html.indexOf('data-slot="select-value"');
    if (start === -1) return '';
    const open = html.indexOf('>', start);
    const close = html.indexOf('</span>', open);
    return html.slice(open + 1, close === -1 ? undefined : close);
}

describe('下拉的当前值(默认态必须看得见"跟随服务器")', () => {
    // 修前的真问题: Radix 的 SelectValue 对空串 value 只渲染 placeholder, 永远不去取选中项文案
    // (源码 shouldShowPlaceholder: value === '' || value === undefined)。
    // 而"跟随服务器"在库里的真实取值就是空串, 于是绝大多数用户(从未设置过时区的账号)
    // 打开设置页看到的是一格**空白**, 而不是"跟随服务器"。这不是样式问题: 那一项根本没渲染文案。
    //
    // 注意 SSR 下 zustand 走的是 getInitialState()(服务端快照), 而 store 的初值正是空串,
    // 恰好就是"从未设置过时区的账号"这一态 —— 这条断言钉的正是那个最常见的默认态。
    it.each([['en', en], ['zh-Hans', hans], ['zh-Hant', hant]] as const)('%s 下触发格里是"跟随服务器", 不是空白', (_locale, messages) => {
        const html = renderSSR(createElement(TimezoneSetting), undefined, { locale: _locale, messages });
        expect(valueSpanText(html).trim()).toBe(messages.setting.account.timezone.server);
    });

    it('触发格里不是空的(修前的形态: 空的 select-value)', () => {
        // 修前实测就是这一串:<span data-slot="select-value" style="pointer-events:none"></span>
        // 单独再钉一条"非空", 免得文案断言被"某一项恰好叫空串"绕过去。
        expect(valueSpanText(renderSSR(createElement(TimezoneSetting))).trim().length).toBeGreaterThan(0);
    });
});

describe('下拉取值与库值互转(哨兵)', () => {
    it('库里的空串映射成哨兵, 其余原样', () => {
        // 换成哨兵的原因见 SERVER_OPTION_VALUE 注释: 空串当 value 时 Radix 不渲染文案。
        expect(timezoneToOptionValue('')).toBe(SERVER_OPTION_VALUE);
        expect(timezoneToOptionValue('Asia/Shanghai')).toBe('Asia/Shanghai');
    });

    it('哨兵映射回空串, 其余原样(提交前必须过这一道)', () => {
        // 直接把 '@server' 发出去会被后端 NormalizeTimezone 判为非法时区名, 用户看到"保存失败"。
        expect(optionValueToTimezone(SERVER_OPTION_VALUE)).toBe('');
        expect(optionValueToTimezone('Asia/Shanghai')).toBe('Asia/Shanghai');
    });

    it('哨兵与任何真实时区名都不会撞', () => {
        // '@' 不出现在 IANA 名里(形如 Area/Location 或 UTC 这类缩写), 所以哨兵是安全的。
        expect(isValidTimezoneName(SERVER_OPTION_VALUE)).toBe(false);
        expect(SERVER_OPTION_VALUE).not.toBe('UTC');
        expect(() => new Intl.DateTimeFormat('en-US', { timeZone: SERVER_OPTION_VALUE })).toThrow();
    });
});

describe('账户设置里的时区行', () => {
    // 纯逻辑再对, 组件没接上去也等于没做: 这里证明这一行真的出现在账户设置里,
    // 且位置在"修改密码"之后、"退出登录"之前 —— 任务描述要求的就是这个位置。

    it('账户卡片里出现时区标题与说明', () => {
        const html = renderSSR(createElement(SettingAccount));
        expect(html).toContain(en.setting.account.timezone.label);
        expect(html).toContain(en.setting.account.timezone.hint);
    });

    it('位置在修改密码之后、退出登录之前', () => {
        // 顺序错了不会报错, 只会让"退出登录"被挤到中间 —— 那是最容易误点的一处。
        const html = renderSSR(createElement(SettingAccount));
        const password = html.indexOf(en.setting.account.password.label);
        const timezone = html.indexOf(en.setting.account.timezone.label);
        const logout = html.indexOf(en.setting.account.logout.label);
        expect(password).toBeGreaterThanOrEqual(0);
        expect(timezone).toBeGreaterThan(password);
        expect(logout).toBeGreaterThan(timezone);
    });

    it('下拉的可访问名就是时区标题(读屏用户能说出这格是什么)', () => {
        const html = renderSSR(createElement(SettingAccount));
        expect(html).toContain('id="account-timezone"');
        expect(html).toContain('aria-label="' + en.setting.account.timezone.label + '"');
    });

    it.each([['zh-Hans', hans], ['zh-Hant', hant]] as const)('%s 下不出现原始 i18n 路径', (_locale, messages) => {
        // 缺键时 use-intl 会把 setting.account.timezone.label 原样画在界面上。
        const html = renderSSR(createElement(SettingAccount), undefined, { locale: _locale, messages });
        expect(html).not.toContain('setting.account.timezone');
        expect(html).toContain(messages.setting.account.timezone.label);
        expect(html).toContain(messages.setting.account.timezone.hint);
    });
});

describe('时区偏好与认证状态', () => {
    it('setAuth 从 profile 写入 timezone, 缺省写成空串(跟随服务器)', () => {
        useAuthStore.getState().setAuth({ id: 1, role: 'admin', username: 'a', email: '', status: 'active' } as never);
        // 缺省必须是空串而不是 undefined: 受控 Select 的 value 给 undefined 会变成非受控,
        // 之后用户选什么都看不出变化。
        expect(useAuthStore.getState().timezone).toBe('');
        useAuthStore.getState().setAuth({ id: 1, role: 'admin', timezone: 'Asia/Shanghai' } as never);
        expect(useAuthStore.getState().timezone).toBe('Asia/Shanghai');
    });

    it('checkAuth 以服务端为准刷新 timezone(另一台设备改过的值能同步过来)', async () => {
        vi.stubGlobal('fetch', vi.fn().mockImplementation(() =>
            Promise.resolve(new Response(JSON.stringify({ data: { id: 9, role: 'user', timezone: 'Europe/Berlin' } }))),
        ));
        useAuthStore.getState().setAuth({ id: 9, role: 'user', timezone: 'Asia/Shanghai' } as never);
        await useAuthStore.getState().checkAuth();
        expect(useAuthStore.getState().timezone).toBe('Europe/Berlin');
    });

    it('logout 清空 timezone, 不把上一个账号的偏好留给下一个人', () => {
        useAuthStore.getState().setAuth({ id: 1, role: 'admin', timezone: 'America/New_York' } as never);
        useAuthStore.getState().logout();
        expect(useAuthStore.getState().timezone).toBe('');
    });

    it('API key 认证下没有账号侧时区, 清成空串', () => {
        useAuthStore.getState().setAuth({ id: 1, role: 'admin', timezone: 'Asia/Shanghai' } as never);
        useAuthStore.getState().setAPIKeyAuth('sk-1');
        expect(useAuthStore.getState().timezone).toBe('');
    });

    it('timezone 不进 persist: 只持久化 token/isAPIKeyAuth/role', () => {
        // 持久化只会留下过期值 —— 用户在另一台设备改完时区后, 这台设备会一直显示旧选择。
        const partialize = useAuthStore.persist.getOptions().partialize!;
        const persisted = partialize(useAuthStore.getState()) as Record<string, unknown>;
        expect(Object.keys(persisted).sort()).toEqual(['isAPIKeyAuth', 'role', 'token']);
        expect(persisted.timezone).toBeUndefined();
    });
});

describe('useUpdateTimezone', () => {
    it('提交体是 { timezone }, 地址是 POST /api/v1/user/timezone', async () => {
        // 只断言本地 store 的话, 请求路径写错也照样绿 —— 而写错就是"点了保存但没存上"。
        const fetch = vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ data: 'Asia/Shanghai' }))));
        vi.stubGlobal('fetch', fetch);
        useAuthStore.getState().setAuth({ id: 1, role: 'admin', timezone: '' } as never);
        await captureUpdateTimezone().mutateAsync({ timezone: 'Asia/Shanghai' });
        // apiRequest 用 new URL(path, document.baseURI) 解析, 传给 fetch 的已经是绝对地址;
        // 断言 pathname 即可 —— 这里要抓的是

        // 路径写错(例如写成 user/timezones 少个 s), 而不是主机名的拼法。
        const [calledTarget, calledInit] = fetch.mock.calls[0] as unknown as [URL | string, RequestInit];
        expect(new URL(String(calledTarget)).pathname).toBe('/api/v1/user/timezone');
        expect(calledInit.method).toBe('POST');
        // 请求体字段名必须与后端一致(timezone, 不是 tz / time_zone):
        // 写错时后端会当成"没传"而静默用默认值, 用户看到"已保存"但下次打开还是旧值。
        expect(JSON.parse(calledInit.body as string)).toEqual({ timezone: 'Asia/Shanghai' });
    });

    it('成功后就地更新 store, 不需要重新登录', async () => {
        // 改时区不影响 token: 若像改用户名那样强制重登, 用户选个时区就被踢出去。
        // 后端回显归一后的时区名(见 handlers/user.go changeTimezone)。
        vi.stubGlobal('fetch', vi.fn().mockImplementation(() =>
            Promise.resolve(new Response(JSON.stringify({ data: 'America/New_York' }))),
        ));
        useAuthStore.getState().setAuth({ id: 1, role: 'admin', timezone: '' } as never);
        await captureUpdateTimezone().mutateAsync({ timezone: 'America/New_York' });
        expect(useAuthStore.getState().timezone).toBe('America/New_York');
        expect(useAuthStore.getState().isAuthenticated).toBe(true);
    });

    it('服务端回显归一后的名字时, store 存回显值而不是提交值', async () => {
        // 后端把 asia/shanghai 归一成 Asia/Shanghai 再回传。若直接把提交值写进 store,
        // 界面显示与库里就不一致, 下一次 checkAuth 会"莫名其妙地变了个写法"。
        vi.stubGlobal('fetch', vi.fn().mockImplementation(() =>
            Promise.resolve(new Response(JSON.stringify({ data: 'Asia/Shanghai' }))),
        ));
        useAuthStore.getState().setAuth({ id: 1, role: 'admin', timezone: '' } as never);
        await captureUpdateTimezone().mutateAsync({ timezone: 'asia/shanghai' });
        expect(useAuthStore.getState().timezone).toBe('Asia/Shanghai');
    });

    it('失败时不改 store(界面上的选择必须退回真值)', async () => {
        vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('boom')));
        useAuthStore.getState().setAuth({ id: 1, role: 'admin', timezone: 'Asia/Shanghai' } as never);
        await expect(captureUpdateTimezone().mutateAsync({ timezone: 'Europe/Berlin' })).rejects.toThrow('boom');
        // 受控值直接读 store, 所以"没改成"就等于界面自动退回旧值 —— 用户不会带着错误印象离开。
        expect(useAuthStore.getState().timezone).toBe('Asia/Shanghai');
    });
});

// captureUpdateTimezone 渲染一个只取 hook 的组件, 把 mutation 对象拿出来用。
//
// 本仓库没有 jsdom / @testing-library, 也没有 renderHook: 但 mutation 拿到手之后
// 可以脱离渲染直接 mutateAsync, 这样既走了真实的 hook 定义(路径/请求体/onSuccess),
// 又不需要 DOM。
function captureUpdateTimezone() {
    let captured: ReturnType<typeof useUpdateTimezone> | undefined;
    function Probe() {
        captured = useUpdateTimezone();
        return null;
    }
    renderSSR(createElement(Probe));
    if (!captured) throw new Error('hook 未被调用');
    return captured;
}
