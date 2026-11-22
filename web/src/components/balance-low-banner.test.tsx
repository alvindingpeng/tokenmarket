import { describe, expect, it } from 'vitest';
import type { QueryClient } from '@tanstack/react-query';
import { renderSSR } from '@/test/ssr';
import { BalanceLowBanner } from './balance-low-banner';

// 余额不足横幅的 SSR 行为。
//
// 这一组断言的重点不是"提示长得对不对", 而是"什么时候绝对不该说话":
//   加载中 / 失败 / 无预警配置 / 余额 <= 0 —— 这四种情况下横幅必须整段消失,
//   而且绝不能出现 0。资金页上把"没取到"显示成 0, 用户会以为钱被清零了。

const summaryKey = ['wallet', 'summary'] as const;
const alertKey = ['balance-alert'] as const;

// seedError 把一条查询推进到失败态。
// retryOnMount: false 是给 mock SSR 用的: 关掉自动重试, 快照才能停在落定后的错误态,
// 也就是真实 UI 里失败后用户看到的样子(而不是"即将重试"的过渡 pending)。
function seedError(client: QueryClient, queryKey: readonly unknown[]) {
    client.setDefaultOptions({ queries: { retryOnMount: false } });
    client.getQueryCache().build(client, { queryKey }, {
        data: undefined,
        dataUpdateCount: 0,
        dataUpdatedAt: 0,
        error: new Error('boom'),
        errorUpdateCount: 1,
        errorUpdatedAt: Date.now(),
        fetchFailureCount: 1,
        fetchFailureReason: new Error('boom'),
        fetchMeta: null,
        isInvalidated: false,
        status: 'error',
        fetchStatus: 'idle',
    });
}

// seed 预置两份额度: wallet 摘要与预警配置。
const seed = (wallet: unknown, alert: unknown) => (client: QueryClient) => {
    if (wallet === 'error') seedError(client, summaryKey);
    else if (wallet !== undefined) client.setQueryData(summaryKey, wallet);
    if (alert === 'error') seedError(client, alertKey);
    else if (alert !== undefined) client.setQueryData(alertKey, alert);
};

// 一份"已配置预警且已经跌到线下"的样本: 可用 3.5 = 账面 10 - 冻结 6.5, 阈值 5。
const LOW_WALLET = { balance: 10, frozen: 6.5, pool_currency: 'USD' };
const LOW_ALERT = { threshold_cny: 5, enabled: true };

describe('BalanceLowBanner: 该提示时', () => {
    it('可用余额跌到阈值线下时渲染标题、说明与"去充值"', () => {
        const html = renderSSR(<BalanceLowBanner />, seed(LOW_WALLET, LOW_ALERT));
        // 标题与按钮是用户唯一能据以行动的锚点。
        expect(html).toContain('Low balance');
        expect(html).toContain('Top up');
        // 说明里两个数字都必须出现, 且都带池币种: 只有"余额低"而不说低到多少、线在哪, 用户没法判断。
        expect(html).toContain('3.5 USD');   // 可用 10 - 6.5, formatPlainMoney 截尾零
        expect(html).toContain('5 USD');     // 阈值
        // 阈值那档的专属文案(与"没有阈值"的通用文案必须能区分开)。
        expect(html).toContain('at or below your alert threshold');
    });

    it('可用余额恰等于阈值时也算"跌到线下"(<= 而不是 <)', () => {
        // 用户设线的语义是"低到这个数就叫我", 等于时已经到线了。
        const html = renderSSR(<BalanceLowBanner />, seed({ balance: 5, frozen: 0, pool_currency: 'USD' }, { threshold_cny: 5, enabled: true }));
        expect(html).toContain('Low balance');
        expect(html).toContain('5 USD');
    });

    it('可用余额与阈值标同一个币种(阈值虽是 threshold_cny, 单位也是账面币种)', () => {
        // threshold_cny 的名字是历史遗留, 实际单位是账面币种 USD(internal/model/balance_alert.go)。
        // 两个数字若标成不同单位, 用户会以为阈值是另一种货币, 从而误判自己离危险线有多远。
        const html = renderSSR(<BalanceLowBanner />, seed({ balance: 10, frozen: 6.5, pool_currency: 'USD' }, LOW_ALERT));
        expect(html).toContain('3.5 USD');
        expect(html).toContain('5 USD');
        // 阈值绝不能带上别的币种名。
        expect(html).not.toContain('5 CNY');
    });

    it('pool_currency 缺失时不标币种, 金额照常显示(不猜 CNY 也不猜 USD)', () => {
        // 猜币种比不标币种危险得多: 把美元标成人民币会让用户以为额度差 7 倍。
        const html = renderSSR(<BalanceLowBanner />, seed({ balance: 10, frozen: 6.5 }, LOW_ALERT));
        expect(html).toContain('Low balance');
        expect(html).toContain('3.5');
        expect(html).not.toContain('3.5 USD');
        expect(html).not.toContain('3.5 CNY');
    });
});

describe('BalanceLowBanner: 不该提示时', () => {
    it('可用余额高于阈值时整段不渲染', () => {
        const html = renderSSR(<BalanceLowBanner />, seed({ balance: 100, frozen: 0, pool_currency: 'USD' }, LOW_ALERT));
        expect(html).not.toContain('Low balance');
        expect(html).not.toContain('Top up');
    });

    it('余额 <= 0 时不渲染: 那是 402 的语义, 横幅不抢', () => {
        // 硬失败(请求已被后端挡下)由 402 兜底; 横幅再喊一遍会让用户以为钱被清零了。
        const html = renderSSR(<BalanceLowBanner />, seed({ balance: 0, frozen: 0, pool_currency: 'USD' }, LOW_ALERT));
        expect(html).not.toContain('Low balance');
        expect(html).not.toContain('Top up');
        const negative = renderSSR(<BalanceLowBanner />, seed({ balance: 5, frozen: 8, pool_currency: 'USD' }, LOW_ALERT));
        expect(negative).not.toContain('Low balance');
    });

    it('没有预警配置时整段不渲染(不替用户发明一条线)', () => {
        // 取舍见 balance-low.ts: 没有阈值就没有客观判据, 宁可不说。
        for (const alert of [
            { threshold_cny: 0, enabled: false },
            { threshold_cny: 0, enabled: true },   // 脏 half 状态
            { threshold_cny: 5, enabled: false },  // 关掉了
            undefined,                              // 压根没配过
        ]) {
            const html = renderSSR(<BalanceLowBanner />, seed({ balance: 1, frozen: 0, pool_currency: 'USD' }, alert));
            expect(html).not.toContain('Low balance');
            expect(html).not.toContain('Top up');
            // 尤其不能落回"没有阈值"的通用文案: 那一档在当前版本按设计不出现。
            expect(html).not.toContain('top up before requests start failing');
        }
    });
});

describe('BalanceLowBanner: 四态规则(取不到就不说, 绝不显示 0)', () => {
    it('两条查询都在加载中时不渲染, 也不显示 0', () => {
        const html = renderSSR(<BalanceLowBanner />, () => undefined);
        expect(html).not.toContain('Low balance');
        expect(html).not.toContain('Top up');
        expect(html).not.toMatch(/Available 0/);
    });

    it('钱包摘要请求失败时不渲染, 更不把失败当成余额 0', () => {
        const html = renderSSR(<BalanceLowBanner />, seed('error', LOW_ALERT));
        expect(html).not.toContain('Low balance');
        expect(html).not.toMatch(/Available 0/);
        expect(html).not.toContain('0 USD');
    });

    it('预警配置请求失败时不渲染(判不出线在哪就不说)', () => {
        const html = renderSSR(<BalanceLowBanner />, seed(LOW_WALLET, 'error'));
        expect(html).not.toContain('Low balance');
        expect(html).not.toMatch(/Available 0/);
    });

    it('摘要字段缺失/脏数据时不渲染, 不编一个可用余额出来', () => {
        const html = renderSSR(<BalanceLowBanner />, seed({ pool_currency: 'USD' }, LOW_ALERT));
        expect(html).not.toContain('Low balance');
        expect(html).not.toMatch(/Available 0/);
    });
});
