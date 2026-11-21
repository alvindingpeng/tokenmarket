import { describe, expect, it } from 'vitest';
import type { QueryClient } from '@tanstack/react-query';
import { renderSSR } from '@/test/ssr';
import zhHans from '@/locales/zh_hans.json';
import zhHant from '@/locales/zh_hant.json';
import type { ProfitMarginSummary } from '@/api/billing';
import { MarginDashboard } from './index';

// 毛利看板的退款/充值两张卡此前只有类型、没有渲染: 后端一直在返回 refund_amount /
// recharge_amount, 界面上却看不到, 于是"GMV 变小了为什么"只能靠猜。
//
// 这里用 mock SSR 渲染 MarginDashboard(管理员专属区块), 靠预置查询缓存走到"有数据"分支,
// 断言两张卡与口径提示真的出现在标记里。不渲染整个 Billing: 它按角色分支,
// 而 zustand v5 的 SSR 快照取 getInitialState(), 测试里改不动角色。

// useProfitMargin() 默认 from/to 均为空串, 查询键就是下面这个字面量。
const marginKey = ['profit', 'margin', '', ''] as const;

const overall: ProfitMarginSummary = {
    period: 'all',
    user_cost: 98.5, // 净额 GMV
    owner_revenue: 80,
    platform_revenue: 18.5,
    margin_rate: 18.78,
    request_count: 1234,
    refund_amount: 6.5,
    recharge_amount: 20,
};

const trends: ProfitMarginSummary[] = [{ ...overall, period: '20260115' }];

function seed(client: QueryClient) {
    client.setQueryData(marginKey, { overall, trends });
}

describe('毛利看板退款/充值卡片(mock SSR)', () => {
    it('退款与充值两张卡都渲染出真实数值', () => {
        const html = renderSSR(<MarginDashboard />, seed);
        expect(html).toContain('Refunds in period');
        expect(html).toContain('Top-ups in period');
        expect(html).toContain('6.5'); // refund_amount
        expect(html).toContain('20'); // recharge_amount
    });
    it('加载中数值位是占位, 不是假零', () => {
        const html = renderSSR(<MarginDashboard />);
        // AccountCard 用 '--' 占位并给 opacity-40, 明确表达"还没拿到数"。
        expect(html).toContain('--');
        expect(html).toContain('opacity-40');
        // 数值位不得出现 0 或 0.000000 这种兜底假零。
        expect(html).not.toMatch(/tabular-nums[^>]*>0<\/div>/);
    });
    it('净额口径与资金流口径分开标注', () => {
        const html = renderSSR(<MarginDashboard />, seed);
        expect(html).toContain('Net of refunds'); // 消费/成本卡的净额提示
        expect(html).toContain('Cash flow, not margin'); // 退款/充值卡的定位
        expect(html).toContain('refunds are dated by approval'); // 不构成等式的说明
    });

    it('三语渲染同一棵组件树', () => {
        const hans = renderSSR(<MarginDashboard />, seed, { locale: 'zh-Hans', messages: zhHans });
        expect(hans).toContain('期间退款');
        expect(hans).toContain('资金流, 不计毛利');
        expect(hans).toContain('净额');
        const hant = renderSSR(<MarginDashboard />, seed, { locale: 'zh-Hant', messages: zhHant });
        expect(hant).toContain('期間退款');
        expect(hant).toContain('淨額');
    });

    it('英文文案不带中文', () => {
        const html = renderSSR(<MarginDashboard />, seed);
        expect(html).not.toMatch(/[一-鿿]/);
    });
});
