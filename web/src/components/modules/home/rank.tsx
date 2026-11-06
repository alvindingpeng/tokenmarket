import { TrendingUp } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { useChannelStats } from '@/api/channel';
import { useAuthStore } from '@/api/user';
import { useHomeViewStore, type MetricKey } from './store';
import { MetricTabs } from './metric-tabs';
import type { StatsMetricsFormatted } from '@/api/stats';
import { formatCount, formatMoney } from '@/lib/utils';

// 榜单只展示这几项, 渠道可直接复用 api/channel 已算好的 formatted。
type RankMetrics = Pick<
    StatsMetricsFormatted,
    'total_cost' | 'total_token' | 'request_count' | 'request_success' | 'request_failed'
>;

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

// RankCard 渲染单个排行榜: 标题, 维度切换和榜单列表。
function RankCard({
    title,
    items,
    sortMode,
    onSortModeChange,
    hideChannelName,
}: {
    title: string;
    items: RankItem[];
    sortMode: MetricKey;
    onSortModeChange: (value: MetricKey) => void;
    hideChannelName?: boolean;
}) {
    const t = useTranslations('home.rank');
    const ranked = rankItems(items, sortMode);
    // 展示值与排序同字段: 排序按 raw, 展示取同字段的 formatted, 两者必须来自同一维度。
    const displayField = sortMode === 'cost' ? 'total_cost' : sortMode === 'count' ? 'request_count' : 'total_token';

    return (
        <div className="rounded-3xl bg-card text-card-foreground border-border border pt-2 px-4">
            <div className="flex items-center justify-between">
                <h3 className="font-semibold text-base">{title}</h3>
                <MetricTabs value={sortMode} onChange={onSortModeChange} />
            </div>

            {ranked.length === 0 ? (
                <div className="flex flex-col items-center justify-center py-8 text-muted-foreground">
                    <TrendingUp className="w-12 h-12 mb-3 opacity-30" />
                    <p className="text-sm">{t('noData')}</p>
                </div>
            ) : (
                <div className="space-y-3 max-h-[300px] overflow-y-auto">
                    {ranked.map((item, index) => {
                        const successCount = item.formatted.request_success.raw;
                        const totalCount = successCount + item.formatted.request_failed.raw;

                        return (
                            <div key={item.id} className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 py-3">
                                <div className="flex items-center justify-center font-bold text-lg">{index + 1}</div>

                                <div className="min-w-0">
                                    <p className={`font-medium text-sm truncate ${hideChannelName && !item.channelName ? 'select-none blur-[3px]' : ''}`}>
                                        {item.name}
                                    </p>
                                    {item.channelName && (
                                        <p className={`mt-1 truncate text-xs text-muted-foreground ${hideChannelName ? 'select-none blur-[3px]' : ''}`}>
                                            {item.channelName}
                                        </p>
                                    )}
                                    {sortMode === 'count' && (
                                        <div className="flex items-center gap-1 text-xs text-muted-foreground mt-1">
                                            <span>{t('successRate')}:</span>
                                            <span>{(totalCount > 0 ? (successCount / totalCount) * 100 : 0).toFixed(1)}%</span>
                                        </div>
                                    )}
                                </div>

                                <div className="flex items-center gap-1 text-right">
                                    {sortMode === 'count' ? (
                                        <div className="flex items-center gap-1 text-sm font-medium tabular-nums">
                                            <span className="text-accent">
                                                {item.formatted.request_success.formatted.value}
                                                <span className="text-xs text-muted-foreground">
                                                    {item.formatted.request_success.formatted.unit}
                                                </span>
                                            </span>
                                            <span className="text-muted-foreground/40 font-light">/</span>
                                            <span className="text-destructive">
                                                {item.formatted.request_failed.formatted.value}
                                                <span className="text-xs text-muted-foreground">
                                                    {item.formatted.request_failed.formatted.unit}
                                                </span>
                                            </span>
                                        </div>
                                    ) : (
                                        <span className="font-semibold text-base">
                                            {item.formatted[displayField].formatted.value}
                                            <span className="text-xs text-muted-foreground">
                                                {item.formatted[displayField].formatted.unit}
                                            </span>
                                        </span>
                                    )}
                                </div>
                            </div>
                        );
                    })}
                </div>
            )}
        </div>
    );
}

// Rank 并列渠道榜和模型榜, 两榜各自独立排序。
export function Rank() {
    const role = useAuthStore((state) => state.role);
    const hasChannelScope = role !== 'user'; // 用户没有渠道视图, 渠道榜接口对他们拒绝, 不发请求也不渲染。
    const { data: channelStats } = useChannelStats(hasChannelScope);
    const t = useTranslations('home.rank');
    const channelSortMode = useHomeViewStore((state) => state.channelRankSortMode);
    const setChannelSortMode = useHomeViewStore((state) => state.setChannelRankSortMode);
    const modelSortMode = useHomeViewStore((state) => state.modelRankSortMode);
    const setModelSortMode = useHomeViewStore((state) => state.setModelRankSortMode);
    const isChannelNameHidden = useHomeViewStore((state) => state.isChannelNameHidden);

    // 渠道榜展示发布编码(#1001): 用户视角渠道以编码为身份, 真名只有归属者/管理员该看到。
    const channelItems: RankItem[] = (channelStats ?? []).map((channel) => ({
        id: `channel-${channel.channel_id}`,
        name: channel.share_code ? `#${channel.share_code}` : channel.channel_name,
        formatted: channel.formatted,
    }));

    // 模型榜按模型名聚合: 同一模型可能被多个渠道提供, 用户关心的是"这个模型用量多少",
    // 而不是"每个渠道的这份模型用量多少" —— 后者把同一模型拆成好几行, 排名失去意义。
    const modelAgg = new Map<string, RankItem>();
    for (const channel of channelStats ?? []) {
        for (const channelModel of channel.models) {
            const existing = modelAgg.get(channelModel.model_name);
            if (existing) {
                existing.formatted = addMetrics(existing.formatted, channelModel.formatted);
            } else {
                modelAgg.set(channelModel.model_name, {
                    id: `model-${channelModel.model_name}`,
                    name: channelModel.model_name,
                    formatted: channelModel.formatted,
                });
            }
        }
    }
    // 无调用记录的模型不进榜: 渠道同步进来的模型清单包含从未被调用的, 全零行只会稀释排名。
    // 渠道榜不过滤 —— 渠道即使零调用也承载配置意义, 而模型榜是纯用量视角。
    const modelItems = [...modelAgg.values()].filter((item) => item.formatted.request_count.raw > 0);

    if (!hasChannelScope) return null;

    return (
        <div className="grid grid-cols-1 @3xl/home:grid-cols-2 gap-4">
            <RankCard
                title={t('channel')}
                items={channelItems}
                sortMode={channelSortMode}
                onSortModeChange={setChannelSortMode}
                hideChannelName={isChannelNameHidden}
            />
            <RankCard
                title={t('model')}
                items={modelItems}
                sortMode={modelSortMode}
                onSortModeChange={setModelSortMode}
                hideChannelName={isChannelNameHidden}
            />
        </div>
    );
}
