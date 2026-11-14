import type { ChannelKey } from '@/api/channel';

// 账号凭据的状态推导。
//
// 这一段是纯函数, 因为它是"账号是否还能用"的唯一判据, 必须能单独测:
// 定时刷新在后台跑, 界面上没人看得见它成没成, 所以这里的判断错了不会有任何报错,
// 只会让使用者以为"标着正常"的账号其实已经转发不出去了。

// 临期阈值: 定时任务提前 10 分钟刷新(见 op/account_refresh.go 的 accountRefreshLead),
// 界面上标"将到期"比它宽一档 —— 让人有反应时间, 而不是看到红点时才剩几分钟。
export const ACCOUNT_EXPIRING_MS = 24 * 60 * 60 * 1000;

export type AccountState = 'healthy' | 'expiring' | 'manual' | 'invalid';

// accountStateOf 把一条凭据收敛成四态。
//
// 顺序是有意的: 先判 cookie 类(它们本质上就不可能自动续期, 不该被标成"正常"),
// 再判已失效, 最后才看时间。反过来的话, 一个已失效的账号只要到期时间还在未来,
// 就会被显示成"正常" —— 那是最坏的一种误导。
export function accountStateOf(key: ChannelKey, now: number = Date.now()): AccountState {
    // 非账号凭据不该调这个函数; 归到 manual 只是为了有个确定值, 调用方据 account_kind 分支。
    if (key.account_kind === 'cookie') {
        // cookie 类没有 refresh_token, 到期后无法自动恢复, 只能人工重导。
        // 它不区分"临期" —— 临期对它没有意义, 因为提前知道也不能自动续。
        return 'manual';
    }
    if (key.account_kind !== 'oauth') {
        return 'manual';
    }
    // disabled_until 有值表示凭据级熔断生效中: 这个账号此刻转发出去就是失败。
    // 它比"到期时间在未来"更能说明当下能不能用, 所以排在时间判断之前。
    if (key.disabled_until) {
        const until = Date.parse(key.disabled_until);
        // 解析不出来就当作仍在熔断: 宁可多标一个失效, 也不要把坏账号标成正常。
        if (Number.isNaN(until) || until > now) return 'invalid';
    }
    if (!key.token_expires_at) {
        // 没有到期时间的 oauth 账号(比如 Kiro 的 JSON 里本就不带),
        // 只要没被熔断就按正常: 定时任务会用 expires_in 算出到期时间。
        return 'healthy';
    }
    const expires = Date.parse(key.token_expires_at);
    if (Number.isNaN(expires)) return 'healthy';
    if (expires <= now) return 'invalid';
    if (expires - now <= ACCOUNT_EXPIRING_MS) return 'expiring';
    return 'healthy';
}

// remainingText 把剩余有效期说成人话。
// 超过两天的只报天数并省略小时(精确到小时对使用者没有意义),
// 一天以内的报到小时, 一小时以内的报到分钟 —— 越紧急越要精确。
export type Remaining = { minutes: number } | { hours: number } | { days: number };

// 返回结构化数值而不是拼好的字符串: 界面要按当前语言取词, 在这里拼死一句话就没法本地化了。
export function remainingOf(expiresAt: string | undefined, now: number = Date.now()): Remaining | null {
    if (!expiresAt) return null;
    const expires = Date.parse(expiresAt);
    if (Number.isNaN(expires)) return null;
    const ms = expires - now;
    if (ms <= 0) return null;
    const minutes = Math.floor(ms / 60000);
    if (minutes < 60) return { minutes };
    const hours = Math.floor(minutes / 60);
    if (hours < 48) return { hours };
    return { days: Math.floor(hours / 24) };
}
