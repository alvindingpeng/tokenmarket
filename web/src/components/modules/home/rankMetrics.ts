import type { StatsHomeRankFormatted } from '@/api/stats';
import type { MetricKey } from './store';
import { formatCount, formatMoney } from '@/lib/utils';

// 榜单的纯计算与类型独立成文件: Rank.tsx 只导出组件,
// 混着导出函数会触发 react-refresh/only-export-components, 热更新被判为不可替换。

// 榜单只展示这几项: 聚合接口给的精简结构已按同一口径格式化, 这里直接取用。
export type RankMetrics = StatsHomeRankFormatted;

// 榜单中的一个条目, 渠道和模型共用。
export interface RankItem {
    id: string;
    name: string; // 渠道榜为渠道名, 模型榜为模型名。
    channelName?: string; // 仅模型榜有值; 有值则 name 是模型名, 模糊渠道名时只糊此项。
    formatted: RankMetrics;
}

// addMetrics 两个指标对象逐字段相加: 模型榜聚合同模型跨渠道的统计用。
// 相加后必须对总和重新格式化: 沿用第一个渠道的 formatted.value 会让"显示值"
// 停在首渠道的量级(如 1.20$), 而排序按总和 raw(4.60$) —— 展示与排序肉眼可见地不一致。
export function addMetrics(a: RankMetrics, b: RankMetrics): RankMetrics {
    const sum = (x: RankMetrics[keyof RankMetrics], y: RankMetrics[keyof RankMetrics]) => x.raw + y.raw;
    const cost = formatMoney(sum(a.total_cost, b.total_cost));
    const token = formatCount(sum(a.total_token, b.total_token));
    const count = formatCount(sum(a.request_count, b.request_count));
    const success = formatCount(sum(a.request_success, b.request_success));
    const failed = formatCount(sum(a.request_failed, b.request_failed));
    return {
        total_cost: cost,
        total_token: token,
        request_count: count,
        request_success: success,
        request_failed: failed,
    };
}

// rankItems 按当前维度降序: 抽成纯函数便于回归 —— 排序错了在 UI 上只是行序不对, 肉眼难发现。
export function rankItems(items: RankItem[], sortMode: MetricKey): RankItem[] {
    const sortField = sortMode === 'cost' ? 'total_cost' : sortMode === 'count' ? 'request_count' : 'total_token';
    return [...items].sort((a, b) => b.formatted[sortField].raw - a.formatted[sortField].raw);
}
