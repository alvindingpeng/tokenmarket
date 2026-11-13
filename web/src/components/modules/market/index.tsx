import { useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';
import { ArrowUpAZ, Tag, Timer, TrendingUp } from 'lucide-react';
import { PageActions, usePageActionsStore } from '@/components/common/PageActions';
import { ListErrorState } from '@/components/common/ListErrorState';
import { cn } from '@/lib/utils';
import {
    DEFAULT_MARKET_SORT,
    useMarketCatalog,
    type MarketKind,
    type MarketModel,
    type MarketQuery,
    type MarketSort,
} from '@/api/market';
import { MarketItem } from './Item';
import { useMarketFavorites, useToggleMarketFavorite } from '@/api/marketFavorite';
import { CompareBar, MAX_COMPARE } from './Compare';
import { brandLabel, type MarketTranslate } from './labels';

// MarketActions 向稳定顶栏提供广场的搜索、排序、形态筛选、品牌多选和布局切换。
// 所有条件都进查询参数交给服务端; 前端只负责把条件拼出来, 不自己排一遍。
export function MarketActions() {
    const t = useTranslations('market');
    const searchTerm = usePageActionsStore((state) => state.searchTerms.market || '');
    const layout = usePageActionsStore((state) => state.layouts.market === 'list' ? 'list' : 'grid');
    const sortBy = usePageActionsStore((state) => state.marketSortBy);
    const kind = usePageActionsStore((state) => state.marketKind);
    const brands = usePageActionsStore((state) => state.marketBrands);
    const setSearchTerm = usePageActionsStore((state) => state.setSearchTerm);
    const setLayout = usePageActionsStore((state) => state.setLayout);
    const setSort = usePageActionsStore((state) => state.setMarketSortBy);
    const setKind = usePageActionsStore((state) => state.setMarketKind);
    const toggleBrand = usePageActionsStore((state) => state.toggleMarketBrand);
    const clearBrands = usePageActionsStore((state) => state.clearMarketBrands);
    // 品牌清单来自响应本身, 与正文共用同一份查询缓存, 不额外打接口。
    // 用"不带筛选条件"的基准查询取 brands: 后端在筛选之前汇总, 任意组合下都是全集。
    const { data } = useMarketCatalog(EMPTY_QUERY);
    const availableBrands = useMemo(() => data?.brands ?? [], [data]);
    const tLabel = t as unknown as MarketTranslate;

    return (
        <PageActions
            searchTerm={searchTerm}
            onSearchTermChange={(value) => setSearchTerm('market', value)}
            layout={layout}
            onLayoutChange={(value) => setLayout('market', value)}
            sortOptions={[
                // 四个维度各只有唯一方向: 成功率降、延迟升、价格升、名称升。不给"反转"按钮。
                { value: 'success', label: t('sort.success'), icon: TrendingUp },
                { value: 'latency', label: t('sort.latency'), icon: Timer },
                { value: 'price', label: t('sort.price'), icon: Tag },
                { value: 'name', label: t('sort.name'), icon: ArrowUpAZ },
            ]}
            sortValue={sortBy}
            onSortChange={(value) => {
                if (value === 'success' || value === 'latency' || value === 'price' || value === 'name') {
                    setSort(value as MarketSort);
                }
            }}
            filterOptions={[
                { value: '', label: t('kind.all') },
                { value: 'text', label: t('kind.text') },
                { value: 'image', label: t('kind.image') },
                { value: 'video', label: t('kind.video') },
            ]}
            filterValue={kind}
            onFilterChange={(value) => {
                if (value === '' || value === 'text' || value === 'image' || value === 'video') {
                    setKind(value as MarketKind);
                }
            }}
            extraFilter={(
                <BrandFilter
                    brands={availableBrands}
                    selected={brands}
                    t={tLabel}
                    onToggle={toggleBrand}
                    onClear={clearBrands}
                />
            )}
            // 广场是只读挂牌表: 没有"新建"入口, 加号按钮直接不渲染。
            hideCreate
        >
            {null}
        </PageActions>
    );
}

// EMPTY_QUERY 是不带任何筛选的基准查询, 只用来取 brands 全集(与正文的查询共用缓存条目)。
const EMPTY_QUERY: MarketQuery = { q: '', brands: [], kind: '', sort: DEFAULT_MARKET_SORT };

/** BrandFilter 渲染品牌多选: 单选筛选表达不了"同时看两个厂商", 故单独一块。 */
function BrandFilter({
    brands,
    selected,
    t,
    onToggle,
    onClear,
}: {
    brands: string[];
    selected: string[];
    t: MarketTranslate;
    onToggle: (brand: string) => void;
    onClear: () => void;
}) {
    return (
        <div className="grid gap-2">
            <div className="flex items-center justify-between">
                <p className="text-xs font-medium text-muted-foreground">{t('filter.brand')}</p>
                {selected.length > 0 && (
                    <button
                        type="button"
                        onClick={onClear}
                        className="text-[11px] text-muted-foreground transition-colors hover:text-foreground"
                    >
                        {t('filter.clear')}
                    </button>
                )}
            </div>
            <div className="flex max-h-40 flex-col gap-1 overflow-y-auto">
                {brands.length === 0 && <p className="text-xs text-muted-foreground">{t('filter.noBrand')}</p>}
                {brands.map((brand) => (
                    <button
                        key={brand}
                        type="button"
                        onClick={() => onToggle(brand)}
                        className={cn(
                            'h-8 shrink-0 rounded-lg border px-2 text-left text-xs font-medium transition-colors',
                            selected.includes(brand)
                                ? 'border-primary/30 bg-primary text-primary-foreground'
                                : 'border-border bg-muted/20 text-foreground hover:bg-muted/30'
                        )}
                    >
                        {brandLabel(t, brand)}
                    </button>
                ))}
            </div>
        </div>
    );
}

/** marketQueryOf 把顶栏状态拼成查询条件; 广场的筛选口径完全由它决定, 顶栏与正文共用。 */
function marketQueryOf(searchTerm: string, kind: MarketKind, brands: string[], sort: MarketSort): MarketQuery {
    return { q: searchTerm, brands, kind, sort };
}

/** Market 渲染广场正文。 */
export function Market() {
    const t = useTranslations('market');
    // 对比选择用模型名的下标而不是名字数组: 勾选状态要能在筛选/排序变化后仍然有效,
    // 但重排后同一个模型的下标会变, 所以这里存的是"当前这一屏的全集下标", 并在渲染时
    // 用当前列表反查。见 selectedModels 的注释。
    const [selected, setSelected] = useState<number[]>([]);
    const searchTerm = usePageActionsStore((state) => state.searchTerms.market || '');
    const layout = usePageActionsStore((state) => state.layouts.market === 'list' ? 'list' : 'grid');
    const sortBy = usePageActionsStore((state) => state.marketSortBy);
    const kind = usePageActionsStore((state) => state.marketKind);
    const brands = usePageActionsStore((state) => state.marketBrands);

    const query = useMemo(() => marketQueryOf(searchTerm, kind, brands, sortBy), [searchTerm, kind, brands, sortBy]);
    const { data, isError, refetch, isRefetching } = useMarketCatalog(query);
    const { data: favorites = [] } = useMarketFavorites();
    const toggleFavorite = useToggleMarketFavorite();
    // 只看收藏: 本地开关, 不进服务端查询 —— 收藏集合本身很小(一个用户几十条),
    // 让服务端多一个 favorites_only 参数只为过滤这点数据, 反而多一处需要同步的口径。
    const [onlyFavorites, setOnlyFavorites] = useState(false);

    if (isError) {
        return <ListErrorState onRetry={() => void refetch()} retrying={isRefetching} />;
    }

    const allModels: MarketModel[] = data?.models ?? [];
    const favoriteSet = useMemo(() => new Set(favorites), [favorites]);
    // 先按收藏过滤再进对比: 对比下标基于过滤后的数组, 否则勾选会指到看不见的行。
    const models = onlyFavorites ? allModels.filter((model) => favoriteSet.has(model.name)) : allModels;

    // toggle 按下标切换, 上限 4 个: 再多就无法并排阅读, 也超出人一次能比较的范围。
    const toggle = (index: number) => {
        setSelected((current) => {
            if (current.includes(index)) return current.filter((item) => item !== index);
            if (current.length >= MAX_COMPARE) return current;
            return [...current, index];
        });
    };

    return (
        // 滚动容器必须长在这里: 页面层是 absolute inset-0 + overflow-hidden(切页靠它, 外层不随内容长高),
        // 正文自己不滚就整页锁死。用 VirtualizedGrid 的页面不用管(它自带 h-full overflow-y-auto),
        // 而广场刻意不用虚拟化(卡片可展开), 所以这层要手写 —— 与 analytics/ratelimit 等报价页同一写法。
        <div className="flex h-full min-h-0 flex-col gap-3">
            {/* 概览行定住不滚: 往下翻时还能看见本次结果的模型/挂牌条数。 */}
            <div className="flex shrink-0 flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                <span>{t('summary', { models: models.length, channels: countChannels(models) })}</span>
                <div className="flex items-center gap-3">
                    <label className="flex cursor-pointer items-center gap-1.5">
                        <input
                            type="checkbox"
                            className="size-3.5 accent-amber-500"
                            checked={onlyFavorites}
                            onChange={(event) => setOnlyFavorites(event.target.checked)}
                        />
                        <span className={onlyFavorites ? 'text-amber-500' : undefined}>
                            {t('favorite.only', { count: favorites.length })}
                        </span>
                    </label>
                    <span>{t('refreshNote')}</span>
                </div>
            </div>
            <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden overscroll-contain pb-24 pr-1 md:pb-4">
                {/* 空态要分清"筛完没有"和"本来就没有": 只看收藏时给一句可操作的话, 否则用户会以为页面坏了。 */}
                {models.length === 0 ? (
                    <p className="py-16 text-center text-sm text-muted-foreground">
                        {onlyFavorites && favorites.length > 0 ? t('favorite.empty') : t('empty')}
                    </p>
                ) : (
                    // 不用 VirtualizedGrid: 广场卡片可以展开, 展开后行高变化会让虚拟化的测量失效
                    // (行高只在挂载时测一次), 结果是下一行盖住展开内容。几十行的直接渲染代价远小于这个坑。
                    <div className={cn('grid gap-4', layout === 'list' ? 'grid-cols-1' : 'grid-cols-1 md:grid-cols-2')}>
                        {models.map((model, index) => (
                            <MarketItem
                                key={model.name}
                                model={model}
                                layout={layout}
                                compareIndex={index}
                                compareSelected={selected}
                                onToggleCompare={toggle}
                                favorited={favoriteSet.has(model.name)}
                                onToggleFavorite={(name, favorite) => toggleFavorite.mutate({ model: name, favorite })}
                            />
                        ))}
                    </div>
                )}
            </div>
            <CompareBar models={models} selected={selected} onToggle={toggle} onClear={() => setSelected([])} />
        </div>
    );
}

/** countChannels 统计本次返回的挂牌总数(渠道 × 模型的行数), 用于顶部概览。 */
function countChannels(models: MarketModel[]): number {
    return models.reduce((sum, model) => sum + model.channels.length, 0);
}
