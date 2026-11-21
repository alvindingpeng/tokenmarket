// 时区偏好设置项的逻辑层。
//
// 逻辑与组件分文件是既有的 Fast Refresh 约定: 组件文件只导出组件, 纯函数与单测放这里。
// 文件名刻意不叫 Timezone.* —— 同 basename 的 .ts 与 .tsx 会让 typescript-eslint 的
// projectService 报『同一文件被两个扩展名命中』, 所以组件叫 Timezone.tsx, 逻辑叫 timezoneOptions.ts。

// TimezoneOptionLabel 选项属于哪一类, 决定文案走哪个 key:
//   server —— 跟随服务器时区(后端用服务器本地时区), 存的是空串;
//   browser —— 浏览器探测出来的时区, 只在它还不等于已存值时出现;
//   custom  —— 账号里已存着、但既不等于服务器默认也不等于本浏览器的值(例如在另一台设备上设的)。
export type TimezoneOptionLabel = 'server' | 'browser' | 'custom';

// SERVER_OPTION_VALUE 是「跟随服务器」那一项在下拉里的取值。
//
// 为什么不用空串: Radix 的 SelectValue 有一条硬规则(见 @radix-ui/react-select dist/index.mjs
// shouldShowPlaceholder), value 为空串或 undefined 时它只渲染 placeholder、**永远不去取选中项的文案**。
// 而空串正是"跟随服务器"在库里的真实取值, 于是绝大多数用户(从未设置过时区的账号)
// 打开设置页会看到一格空白 —— 不是灰色的"未选择", 是彻底空的。实测 trigger 里
// <span data-slot="select-value"> 无任何子节点。空串当哨兵在这套控件里根本不可用。
//
// 为什么是 '@server': IANA 时区名里不可能出现 '@'(形如 Area/Location 或 UTC 这类缩写),
// 因此这个值不会与任何真实时区撞上; 后端也永远不会收到它 —— 提交前会映射回空串。
export const SERVER_OPTION_VALUE = '@server';

// timezoneToOptionValue 把"库里的值"映射成"下拉里的值": 空串 -> 哨兵, 其余原样。
export function timezoneToOptionValue(saved: string): string {
    return saved === '' ? SERVER_OPTION_VALUE : saved;
}

// optionValueToTimezone 把"下拉里的值"映射回"库里的值": 哨兵 -> 空串, 其余原样。
// 提交前必须过这一道: 把 '@server' 发出去会被后端 NormalizeTimezone 判为非法时区名。
export function optionValueToTimezone(value: string): string {
    return value === SERVER_OPTION_VALUE ? '' : value;
}

export type TimezoneOption = {
    value: string;
    label: TimezoneOptionLabel;
    // timezone 仅在需要回填到 {tz} 占位符时存在: server 项没有具体时区名(它就是"跟随"本身)。
    timezone?: string;
};

// detectBrowserTimezone 取浏览器所在时区的 IANA 名(如 Asia/Shanghai)。
//
// 拿不到就返回空串, 而不是猜一个或抛出去: 这里只用来"建议"一个选项,
// 失败时退化成只有"跟随服务器"一项完全够用 —— 让设置项因为探测失败而整块不可用才是更糟的结果。
// 会走到这里的情形包括 Node 环境(无 Intl)、老旧浏览器、以及隐私浏览器对外暴露假时区。
export function detectBrowserTimezone(): string {
    try {
        const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
        return typeof tz === 'string' ? tz : '';
    } catch {
        return '';
    }
}

// buildTimezoneOptions 生成下拉项: 恒定第一项是"跟随服务器", 其后按需追加"浏览器"与"已保存"。
//
// 为什么不用一份全世界时区清单: 那有几百项, 用户要在里面滚半天才找到自己那个;
// 而实际会用到的基本只有三种 —— 跟随服务器(默认)、本机时区(最常见的一次性设置)、
// 以及账号里已经存着的那个(可能是别的设备设的, 也可能就是本机但浏览器识别成了别的名字)。
// 三项足够覆盖全部日常情形, 且每项都说清了"这是哪个时区"。
//
// value 必须唯一: 下拉里出现两个同值项时, 选中态会落在第一个, 用户点第二个看起来毫无反应。
//
// **每一项已存的值都必须能在列表里找到对应项**, 否则受控 Select 的 value 匹配不到任何 item,
// 界面会显示成全空 —— 用户存着 Asia/Shanghai, 打开设置却看到"未选择", 以为自己的设置丢了。
// 这正是 saved === browser 时仍要保留 browser 项的原因(它不是重复, 而是同一个值的唯一表示)。
export function buildTimezoneOptions(params: { saved: string; browser: string }): TimezoneOption[] {
    const { saved, browser } = params;
    const options: TimezoneOption[] = [{ value: SERVER_OPTION_VALUE, label: 'server' }];
    // 探测值必须先证明自己是个 Intl 认得的时区名再加进列表: 极少数浏览器/隐私插件会给出
    // 一个 Intl 自己都不认的字符串(或被改写成假时区), 这样的选项一旦被选中, 保存必然失败 ——
    // 用户看到"保存失败"却不知道错在哪, 只能退回服务器时区。与其让他踩一次, 不如根本不给这个选项。
    if (browser !== '' && isValidTimezoneName(browser)) {
        // browser 项同时承担"本机时区"与(当 saved === browser 时)"已保存值"两种角色。
        options.push({ value: browser, label: 'browser', timezone: browser });
    }
    // saved 为空时无需"已保存"项: 它由第一项(哨兵 SERVER_OPTION_VALUE)代表。
    // 已经出现过(saved 就是本机时区, 或上面的 browser 项被跳过但值仍在)就不再追加。
    // 判据写成"列表里还没有这个值"而不是"saved !== browser": 后者在一处边界上会漏项 ——
    // 账号里存着一个 Intl 不认的名字、而浏览器探测又恰好给出同一个名字时,
    // browser 项被合法性挡掉、saved 又因等于 browser 被跳过, 那个值就没有任何一项代表它,
    // 受控 Select 匹配不到 item, 格子显示为空(用户以为自己的设置丢了)。
    if (saved !== '' && !options.some((option) => option.value === saved)) {
        options.push({ value: saved, label: 'custom', timezone: saved });
    }
    return options;
}

// isValidTimezoneName 判断一个时区名能不能被 Intl 认出来。
//
// 前端不做"白名单"式校验: 时区数据库(含历史别名、新旧命名)随浏览器/Node 版本变化,
// 白名单迟早与运行时不一致, 结果是拦掉合法值或放过于值。让 Intl 自己试一次最准。
// 空串按合法处理: 它表示"跟随服务器", 不是一个时区名, 不该拿去构造 DateTimeFormat。
export function isValidTimezoneName(name: string): boolean {
    if (name === '') return true;
    try {
        new Intl.DateTimeFormat('en-US', { timeZone: name });
        return true;
    } catch {
        return false;
    }
}
