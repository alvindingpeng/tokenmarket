import { describe, expect, it, vi } from 'vitest';
import type { QueryClient } from '@tanstack/react-query';
import { statsHomeQueryOptions } from '@/api/queries';
import type { StatsHome } from '@/api/stats';
import { renderSSR } from '@/test/ssr';
import { Rank } from './rank';
import { addMetrics, formatRankSuccessRate, rankItems, type RankItem } from './rankMetrics';

// 排行榜的成功率只在「按次数」维度展示, 而维度存在 zustand(默认按金额)。
// SSR 里 zustand v5 走 getInitialState, setState 对渲染不可见 —— 只能把 store 换成固定返回值的替身,
// 才能把「零样本渠道」这一格渲染出来。替身只钉维度, 其余行为(排序/聚合)仍跑真实代码。
vi.mock('./store', () => ({
    useHomeViewStore: (selector: (state: Record<string, unknown>) => unknown) => selector({
        channelRankSortMode: 'count',
        modelRankSortMode: 'count',
        chartMetricType: 'count',
        chartPeriod: '7',
        isChannelNameHidden: false,
    }),
}));

// 榜单排序回归: 顺序必须跟随当前维度(金额/次数/词元)。
// fixture 刻意让三个维度的第一名不同: gpt-b 赢金额和次数, gpt-a 赢词元 ——
// 若排序读错字段, 词元用例会立刻翻车。
const mkMetrics = (cost: number, tokens: number, count: number): RankItem['formatted'] => ({
    total_cost: { raw: cost, formatted: { value: String(cost), unit: '$' } },
    total_token: { raw: tokens, formatted: { value: String(tokens), unit: '' } },
    request_count: { raw: count, formatted: { value: String(count), unit: '' } },
    request_success: { raw: count, formatted: { value: String(count), unit: '' } },
    request_failed: { raw: 0, formatted: { value: '0', unit: '' } },
});

describe('addMetrics 聚合后重新格式化', () => {
    // 1.2 + 3.4 = 4.6: 若沿用首渠道的显示值, 显示与排序(按总和)肉眼不一致。
    it('显示值反映总和, 不是第一个渠道的值', () => {
        const merged = addMetrics(mkMetrics(1.2, 0, 0), mkMetrics(3.4, 0, 0));
        expect(merged.total_cost.raw).toBeCloseTo(4.6);
        // 显示值取总和的格式化结果(两位小数), 绝不能是首渠道的 '1.2'。
        expect(merged.total_cost.formatted.value).toBe('4.60');
    });
    it('零调用模型聚合保持零', () => {
        const merged = addMetrics(mkMetrics(0, 0, 0), mkMetrics(0, 0, 0));
        expect(merged.request_count.raw).toBe(0);
    });
});

describe('rankItems 排序跟随维度', () => {
    const items: RankItem[] = [
        { id: 'model-gpt-a', name: 'gpt-a', formatted: mkMetrics(2, 900, 5) },
        { id: 'model-gpt-b', name: 'gpt-b', formatted: mkMetrics(20, 100, 50) },
    ];
    const names = (mode: 'cost' | 'count' | 'tokens') => rankItems(items, mode).map((i) => i.name);

    it('按金额: gpt-b(20) 在 gpt-a(2) 前', () => {
        expect(names('cost')).toEqual(['gpt-b', 'gpt-a']);
    });
    it('按次数: gpt-b(50) 在 gpt-a(5) 前', () => {
        expect(names('count')).toEqual(['gpt-b', 'gpt-a']);
    });
    it('按词元: gpt-a(900) 反超到第一', () => {
        expect(names('tokens')).toEqual(['gpt-a', 'gpt-b']);
    });
    it('零调用模型排序稳定(不参与过滤, 过滤在聚合层)', () => {
        const withIdle: RankItem[] = [...items, { id: 'model-idle', name: 'gpt-idle', formatted: mkMetrics(0, 0, 0) }];
        expect(rankItems(withIdle, 'cost').map((i) => i.name)).toEqual(['gpt-b', 'gpt-a', 'gpt-idle']);
    });
});
// 零样本渠道会出现在渠道榜上(渠道榜不过滤零调用), 成功率必须与 analytics 走同一哨兵规则:
// totalCount == 0 显示 "—"。显示 0.0% 会被读成"刚上线就全挂", 与"没有样本"混为一谈。
describe('formatRankSuccessRate 零样本走空态', () => {
    it('成功+失败为 0 时显示 "—"', () => {
        expect(formatRankSuccessRate(0, 0)).toBe('—');
    });
    it('有样本时按一位小数展示真实百分比', () => {
        expect(formatRankSuccessRate(9, 10)).toBe('90.0%');
        expect(formatRankSuccessRate(0, 4)).toBe('0.0%'); // 全失败是真实的 0, 不能抹成 "—"
    });
    it('全部成功也是真实的 100%', () => {
        expect(formatRankSuccessRate(7, 7)).toBe('100.0%');
    });
});
// homeData 只有一个一次转发样本都没有的渠道: 它仍会出现在渠道榜上(渠道榜不过滤零调用),
// 于是「成功率」这一格必须显示 "—" —— 显示 0.0% 会被读成"刚上线就全挂"。
const homeData: StatsHome = {
    total: {
        id: 1,
        input_token: 0,
        output_token: 0,
        input_cost: 0,
        output_cost: 0,
        wait_time: 0,
        request_success: 0,
        request_failed: 0,
    },
    daily: { max_request_count: 0, items: [] },
    hourly: [],
    channels: [
        {
            id: 1,
            name: '#1001',
            channelName: 'main',
            total_cost: 0,
            total_token: 0,
            request_count: 0,
            request_success: 0,
            request_failed: 0,
            models: [],
        },
    ],
};

function seedHome(client: QueryClient) {
    client.setQueryData(statsHomeQueryOptions.queryKey, homeData);
}

describe('Rank 零样本渠道的成功率单元格', () => {

    it('零转发样本时显示 "—", 而不是 0.0%', () => {
        const html = renderSSR(<Rank />, seedHome);
        expect(html).toContain('data-testid="rank-success-rate"');
        expect(html).toContain('—');
        expect(html).not.toContain('0.0%');
    });

    it('有转发样本时仍展示真实百分比(85.7%)', () => {
        // 6 / 7 = 85.714…: 全失败/全成功之外的普通样本不能被哨兵规则误伤。
        const withSamples: StatsHome = {
            ...homeData,
            channels: [{ ...homeData.channels[0], request_count: 7, request_success: 6, request_failed: 1 }],
        };
        const html = renderSSR(<Rank />, (client) => client.setQueryData(statsHomeQueryOptions.queryKey, withSamples));
        expect(html).toContain('85.7%');
        expect(html).toContain('data-testid="rank-success-rate"');
    });
});
