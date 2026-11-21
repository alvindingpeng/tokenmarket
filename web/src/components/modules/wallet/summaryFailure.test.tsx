import { describe, expect, it } from 'vitest';
import type { QueryClient } from '@tanstack/react-query';
import { renderSSR } from '@/test/ssr';
import zhHans from '@/locales/zh_hans.json';
import Wallet from './index';

// 钱包页摘要取数失败(mock SSR)。
//
// 修前的症状: 五格余额卡片写的是 formatAmount(card.value ?? 0) —— 摘要请求失败时
// 五格全渲染成 0.00。资金页上这是最危险的一种假象: 用户会以为自己的钱被清零了,
// 而真相只是这一次接口调用挂了。
//
// 所以这一组断言的不只是「显示了错误」, 而是「**没有**显示 0」—— 后者才是修前的那条缺陷。
//
// 另一半: 0 仍然必须能显示成 0。余额真的是 0 与「这次没取到」在界面上必须不同,
// 但两者都不许被抹成同一个东西。

const summaryKey = ['wallet', 'summary'] as const;
const ledgerKey = ['wallet', 'ledger', '', 50, 0, 0] as const;

// seedError 把摘要推进到失败态; 钱包页还有一条流水查询要先落定, 否则整页停在加载骨架。
function seedSummaryError(client: QueryClient) {
    client.setDefaultOptions({ queries: { retryOnMount: false } });
    client.getQueryCache().build(client, { queryKey: summaryKey }, {
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
    client.setQueryData(ledgerKey, { items: [], total: 0 });
}

// WALLET_SUMMARY 各字段取互不相同的正数: 任何一处接错都会被下面的断言抓到。
const summaryData = {
    balance: 11.11,
    frozen: 22.22,
    total_recharged: 33.33,
    total_spent: 44.44,
    total_revenue: 55.55,
};

describe('钱包页摘要失败', () => {
    it('摘要取数失败时五格余额不渲染成 0', () => {
        const html = renderSSR(<Wallet />, seedSummaryError);
        // 修前这五格的 title 与正文都是 0.00 —— 用户会以为账户被清零。
        // 断言用 title="0.00" 而不是裸的 0.00: 页面别处还有 10.00 这类占位符, 裸串会误报。
        expect(html).not.toContain('title="0.00"');
        // 占位符才是正确表达: 取不到就说取不到。五格全都要是占位符, 不能漏一格。
        expect(html.split('title="—"').length - 1).toBe(5);
    });

    it('标题下出现错误条与重试按钮, 且文案说明这是取数失败', () => {
        const html = renderSSR(<Wallet />, seedSummaryError);
        expect(html).toContain('Balance could not be loaded');
        expect(html).toContain('Retry');
        // role=alert 是给读屏软件的信号: 这不是静态说明, 是刚发生的事。
        expect(html).toContain('role="alert"');
    });

    it('中文环境下错误条接上了中文文案', () => {
        const html = renderSSR(<Wallet />, seedSummaryError, { locale: 'zh-Hans', messages: zhHans });
        expect(html).not.toContain('wallet.summaryFailed');
        expect(html).toContain('余额加载失败');
    });
});

describe('钱包页摘要正常', () => {
    it('有数据时五格显示真实数值, 且没有错误条', () => {
        const html = renderSSR(<Wallet />, (client) => {
            client.setQueryData(summaryKey, summaryData);
            client.setQueryData(ledgerKey, { items: [], total: 0 });
        });
        expect(html).toContain('11.11');
        expect(html).toContain('55.55');
        expect(html).not.toContain('Balance could not be loaded');
    });

    it('余额真的是 0 时显示 0, **不是**占位符', () => {
        // 反向保住: 把 0 也弄成「—」就反了 —— 「账户确实是空的」是一个具体事实,
        // 与「这次没取到」在要不要报警/要不要充值上的结论完全不同。
        const html = renderSSR(<Wallet />, (client) => {
            client.setQueryData(summaryKey, {
                balance: 0, frozen: 0, total_recharged: 0, total_spent: 0, total_revenue: 0,
            });
            client.setQueryData(ledgerKey, { items: [], total: 0 });
        });
        expect(html).toContain('0.00');
    });
});
