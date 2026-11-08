import { describe, expect, it } from 'vitest';
import type { QueryClient } from '@tanstack/react-query';
import { renderSSR } from '@/test/ssr';
import { marketCatalogQueryOptions } from '@/api/market';
import { TooltipProvider } from '@/components/ui/tooltip';
import { Market } from './index';

// 页面状态回归(mock SSR): 只看结构不依赖网络 —— 这里锁的是「正文必须有滚动出口」这条硬约束。
// 起因是实打实的生产反馈: 广场整页滚不动。页面层是 absolute inset-0 + overflow-hidden
// (切页靠它, 外层不随内容长高), 正文自己不滚就锁死; 用 VirtualizedGrid 的页面自带滚动容器,
// 而广场刻意不用虚拟化(卡片可展开), 所以这层必须手写。

// marketData 是两条模型的最小目录: 一条成绩可信, 一条停用且未定价(用来确认徽章与口径分支)。
const marketData = {
    models: [
        {
            name: 'claude-sonnet-5', brand: 'anthropic', in_groups: ['default'],
            samples: 396, success: 0.999, wait_ms: 5111.7, metrics_source: 'live' as const,
            channels: [{
                channel_grant_id: 1, channel_model_id: 11, channel_code: '#1001', available: true,
                user_price: { input: 0.006, output: 0.03, cache_read: 0.0006, cache_write: 0.0075 },
                price_currency: 'USD', samples: 396, success: 0.999, wait_ms: 5111.7, metrics_source: 'live' as const,
            }],
        },
        {
            name: 'step-5', brand: 'stepfun', in_groups: [],
            samples: 0, metrics_source: 'insufficient' as const,
            channels: [{
                channel_grant_id: 2, channel_model_id: 12, channel_code: '#1011', available: false,
                status_reason: 'disabled' as const, unpriced: true,
                user_price: { input: 0, output: 0, cache_read: 0, cache_write: 0 },
                price_currency: 'USD', samples: 0, metrics_source: 'none' as const,
            }],
        },
    ],
    brands: ['anthropic', 'stepfun'],
    updated_at: '2026-11-08T17:00:00+08:00',
};

// seed 把目录写进查询缓存, 让 SSR 渲染直接命中数据而不是加载态。
const seed = (client: QueryClient) => {
    client.setQueryData(marketCatalogQueryOptions({ q: '', brands: [], kind: '', sort: 'success' }).queryKey, marketData);
};

// page 包一层 TooltipProvider: MarketItem 的模型名带 tooltip, 浏览器里由 main.tsx 全局提供,
// SSR 测试得自己给 —— 缺了它会直接抛 'Tooltip must be used within TooltipProvider'。
const page = (<TooltipProvider><Market /></TooltipProvider>);

describe('Market 页面状态(mock SSR)', () => {
    it('正文自带滚动容器, 整页不会因为内容溢出而锁死', () => {
        const html = renderSSR(page, seed);
        // 有数据时必须出现可滚动的正文容器 —— 缺了它, 内容溢出到 absolute inset-0 之外就再也滚不到。
        expect(html).toContain('overflow-y-auto');
        expect(html).toContain('overscroll-contain');
        // 底部留白: 卡片贴到视口底边时最后一个展开按钮会压在安全区上。
        expect(html).toContain('pb-24');
    });

    it('渲染真值而不是加载占位', () => {
        const html = renderSSR(page, seed);
        expect(html).toContain('claude-sonnet-5');
        // 品牌标签首字母大写(Anthropic), 断言用不区分大小写匹配: 文案大小写不是契约。
        expect(html.toLowerCase()).toContain('anthropic');
        expect(html).not.toContain('Loading...');
    });

    it('样本不足不给数字: 不渲染假的 0% 成功率', () => {
        const html = renderSSR(page, seed);
        // insufficient 档不报数字, 只报样本数。若这里出现 0.0% 就是把未知读成了结论。
        expect(html).not.toContain('0.0%');
    });
});
