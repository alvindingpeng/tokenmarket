import { describe, expect, it } from 'vitest';
import type { QueryClient } from '@tanstack/react-query';
import { statsHomeQueryOptions } from '@/api/queries';
import type { StatsHome } from '@/api/stats';
import { renderSSR } from '@/test/ssr';
import { Total } from './total';

// 首页口径回归(mock SSR): 请求数格必须自带口径说明, 且只补说明不动数值。
// 假零(0.0%)会被读成"全部失败", 比留空更难纠正 —— 用户不会去怀疑一个数字。

// homeData 的请求数是 8 成功 + 2 失败 = 10: 这一格必须含失败(调用口径), 不是计费笔数。
// 趋势/榜单数据留空并不影响本断言 —— 只钉「请求数格怎么说清楚自己」。
const homeData: StatsHome = {
    total: {
        id: 1,
        input_token: 1000,
        output_token: 200,
        input_cost: 0.1,
        output_cost: 0.02,
        wait_time: 1200,
        request_success: 8,
        request_failed: 2,
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

// seedHome 沿用组件取数的同一条查询定义: queryKey 由 queries.ts 提供, 改键名不会让测试悄悄失效。
function seedHome(client: QueryClient) {
    client.setQueryData(statsHomeQueryOptions.queryKey, homeData);
}

describe('Total 请求数格的口径说明', () => {
    it('请求数格带一行说明: 这一格含失败请求', () => {
        const html = renderSSR(<Total />, seedHome);
        expect(html).toContain('data-testid="request-count-scope"');
        expect(html).toContain('Includes failed calls');
    });

    it('数值本身不变: 请求数仍是成功 + 失败', () => {
        // 只补说明、不动数值逻辑 —— 这一格原本就是 8 + 2。
        const html = renderSSR(<Total />, seedHome);
        expect(html).toContain('10');
    });
});
