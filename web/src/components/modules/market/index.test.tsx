import { describe, expect, it } from 'vitest';
import type { QueryClient } from '@tanstack/react-query';
import { createTranslator } from 'use-intl';
import { renderSSR } from '@/test/ssr';
import en from '@/locales/en.json';
import { marketCatalogQueryOptions } from '@/api/market';
import { TooltipProvider } from '@/components/ui/tooltip';
import { Market } from './index';
import { metricsNote, qualityBandTexts } from './labels';

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
            // 第三条: 本进程没跑过, 数字来自近 7 天持久化统计(metrics_source = daily)。
            // 它的存在是为了确认界面不会因为"不是 live"就把 daily 也显示成 —。
            name: 'deepseek-v4-flash', brand: 'deepseek', in_groups: [],
            samples: 120, success: 0.976, wait_ms: 1800, metrics_source: 'daily' as const,
            channels: [{
                channel_grant_id: 3, channel_model_id: 13, channel_code: '#1025', available: true,
                user_price: { input: 0.003, output: 0.012, cache_read: 0.0003, cache_write: 0.004 },
                price_currency: 'USD', samples: 120, success: 0.976, wait_ms: 1800, metrics_source: 'daily' as const,
                quality_band: { success: '95%+', latency: '<2s', uptime: '99.9%+' },
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
    brands: ['anthropic', 'deepseek', 'stepfun'],
    updated_at: '2026-11-08T17:00:00+08:00',
};

// seed 把目录写进查询缓存, 让 SSR 渲染直接命中数据而不是加载态。
const seed = (client: QueryClient) => {
    client.setQueryData(marketCatalogQueryOptions({ q: '', brands: [], kind: '', sort: 'success' }).queryKey, marketData);
};

// page 包一层 TooltipProvider: MarketItem 的模型名带 tooltip, 浏览器里由 main.tsx 全局提供,
// SSR 测试得自己给 —— 缺了它会直接抛 'Tooltip must be used within TooltipProvider'。
const page = (<TooltipProvider><Market /></TooltipProvider>);

// t 是直接读英文文案的翻译器: 口径文案只在展开区出现, SSR 断言不到, 故对纯函数做断言。
const t = createTranslator({ locale: 'en', messages: en, namespace: 'market' }) as unknown as Parameters<typeof metricsNote>[0];

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

    it('日级回落(daily)照样显示数字, 而不是 —', () => {
        const html = renderSSR(page, seed);
        // daily 有达标样本, 只是来源不同 —— 显示成 — 会让重启后的整屏模型看起来"没数据"。
        // 97.6% 是这条用例的证据: 数字确实被渲染出来了。
        expect(html).toContain('97.6%');
    });
});

// 口径文案与质量带的断言放在纯函数上: 两者只在展开后可见, SSR 打不到展开区,
// 但它们恰恰是界面对用户的关键承诺(实时 / 近一周 / 没数据), 必须有测试盯着。
describe('market 口径文案与质量带(mock SSR)', () => {
    it('daily 的说明与 live 不同: 不能说成实时', () => {
        // 用 use-intl 的英文文案对照。daily 必须带 "7 days", live 必须带 "In-process"。
        const daily = metricsNote(t, 'daily', 120);
        const live = metricsNote(t, 'live', 396);
        expect(daily).toContain('7 days');
        expect(live).toContain('In-process');
        expect(daily).not.toContain('In-process');
    });

    it('样本不足与无样本各有说法, 含样本数', () => {
        expect(metricsNote(t, 'insufficient', 7)).toContain('7');
        expect(metricsNote(t, 'insufficient', 7)).toContain('30');
        expect(metricsNote(t, 'none', 0)).toContain('No samples');
    });

    it('质量带按维度拼装, 缺哪个就不出哪个', () => {
        // 三条齐全。
        expect(qualityBandTexts({ success: '95%+', latency: '<2s', uptime: '99.9%+' }))
            .toEqual(['95%+', '<2s', '99.9%+']);
        // 只探测不足(可用率缺失)时仍给出前两条, 不整段抹掉。
        expect(qualityBandTexts({ success: '95%+', latency: '<2s' })).toEqual(['95%+', '<2s']);
        // 全空返回空数组: 调用方据此不渲染空壳。
        expect(qualityBandTexts({})).toEqual([]);
        expect(qualityBandTexts(undefined)).toEqual([]);
    });
});
