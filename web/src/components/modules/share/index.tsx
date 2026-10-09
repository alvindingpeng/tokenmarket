import { useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Copy, PackageCheck, Wand2 } from 'lucide-react';
import { toast } from 'sonner';
import { useChannelStats, type ChannelStatsFormatted } from '@/api/channel';
import { useChannelListings, usePublishChannel, useUpdateChannelListings, type ChannelModelListing } from '@/api/share';
import { useModelList } from '@/api/model';
import { useSettingList } from '@/api/setting';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { formatPlainMoney } from '@/lib/money';
import { markupOf } from '@/lib/pricing';
import { cn } from '@/lib/utils';
import {
    MorphingDialog,
    MorphingDialogTrigger,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogDescription,
} from '@/components/ui/morphing-dialog';

// 金额展示: 计费金额可能极小(不足 0.001), 截断尾零并最多保留 6 位小数(实现见 lib/money)。
const formatMoney = formatPlainMoney;

// ListingRows 渠道模型改价工作台: 搜索与筛选定位模型, 勾选上架并按四类单价设置供货价。
// 除供货价外同时展示"用户价"(供货价 × (1 + 上浮比例)), 让改价的人当场看到用户实际付多少,
// 而不必只盯着自己的收入口径估算。已上架但四价全 0 的行由后端拒绝, 这里提前标红并拦住保存。
function ListingRows({ channelID }: { channelID: number }) {
    const t = useTranslations('share.listing');
    const { data, isLoading } = useChannelListings(channelID);
    const updateListings = useUpdateChannelListings();
    const { data: settings } = useSettingList();
    const markup = useMemo(() => markupOf(settings), [settings]);
    // 草稿是本弹窗内可编辑的一份完整清单, 保存时整份提交; 未改动的行也提交, 后端按名称对齐。
    const [draft, setDraft] = useState<ChannelModelListing[] | null>(null);
    const [keyword, setKeyword] = useState('');
    const [filter, setFilter] = useState<'all' | 'listed' | 'unlisted' | 'unpriced'>('all');
    const rows = draft ?? data ?? [];
    // 参考价来自全局价格表(models.dev 自动同步 + 管理员维护), 用于上架定价快速预填。
    const { data: refModels } = useModelList();
    const refPrices = useMemo(() => {
        const map = new Map<string, ChannelModelListing['supply_price']>();
        (refModels ?? []).forEach((m) => map.set(m.name.toLowerCase(), {
            input: m.input,
            output: m.output,
            cache_read: m.cache_read,
            cache_write: m.cache_write,
        }));
        return map;
    }, [refModels]);

    // unpriced 判定沿用后端口径: 四价全 0 且无媒体价(每张/每秒/分档)即未定价。
    // 生图模型(kind=image)以每张价为准, 四类 token 价可以保持 0。
    const hasMediaPrice = (row: ChannelModelListing) => {
        const media = row.media_supply ?? {};
        return !!(media.per_image || media.per_second || (media.resolution && Object.values(media.resolution).some((v) => v > 0)));
    };
    const isUnpriced = (row: ChannelModelListing) =>
        !row.supply_price.input && !row.supply_price.output && !row.supply_price.cache_read && !row.supply_price.cache_write && !hasMediaPrice(row);

    // visible 是筛选后的展示集, 但保存仍提交全量 rows —— 筛选只是视图, 不能成为漏改价格的原因。
    const visible = useMemo(() => {
        const lower = keyword.trim().toLowerCase();
        return rows.filter((row) => {
            if (lower && !row.name.toLowerCase().includes(lower)) return false;
            if (filter === 'listed') return row.listed;
            if (filter === 'unlisted') return !row.listed;
            if (filter === 'unpriced') return isUnpriced(row);
            return true;
        });
    }, [rows, keyword, filter]);

    // changedCount 是相对服务端数据的真实改动数: 有改动才允许保存, 避免无谓写库与无谓审计。
    const changedCount = useMemo(() => {
        const byName = new Map((data ?? []).map((row) => [row.name, row]));
        return rows.filter((row) => {
            const old = byName.get(row.name);
            if (!old) return false;
            return old.listed !== row.listed ||
                old.supply_price.input !== row.supply_price.input ||
                old.supply_price.output !== row.supply_price.output ||
                old.supply_price.cache_read !== row.supply_price.cache_read ||
                old.supply_price.cache_write !== row.supply_price.cache_write ||
                (old.media_supply?.per_image ?? 0) !== (row.media_supply?.per_image ?? 0) ||
                (old.media_supply?.per_second ?? 0) !== (row.media_supply?.per_second ?? 0);
        }).length;
    }, [rows, data]);

    // zeroPricedListed 是会被后端拒绝的行, 提前算出来用于禁用保存并给出可执行提示。
    const zeroPricedListed = useMemo(
        () => rows.filter((row) => row.listed && isUnpriced(row)).map((row) => row.name),
        [rows]
    );

    const mutateRow = (index: number, patch: Partial<ChannelModelListing>) => {
        const next = rows.map((row, i) => (i === index ? { ...row, ...patch } : row));
        setDraft(next);
    };

    // fillRow 将单行四类单价替换为参考价; 未匹配到参考价时提示。
    // 生图模型按张计价, 参考价表是 token 口径, 不适用。
    const fillRow = (index: number) => {
        if (rows[index].kind === 'image') {
            toast.error(t('toast.fillNoRef'));
            return;
        }
        const ref = refPrices.get(rows[index].name.toLowerCase());
        if (!ref) {
            toast.error(t('toast.fillNoRef'));
            return;
        }
        mutateRow(index, { supply_price: { ...ref } });
    };

    // fillUnpriced 批量填充四价全为 0 的行, 不覆盖已手填的价格; 生图行按张计价, 跳过。
    const fillUnpriced = () => {
        let count = 0;
        const next = rows.map((row) => {
            const p = row.supply_price;
            const unpriced = !p.input && !p.output && !p.cache_read && !p.cache_write;
            const ref = refPrices.get(row.name.toLowerCase());
            if (!unpriced || !ref || row.kind === 'image') {
                return row;
            }
            count += 1;
            return { ...row, supply_price: { ...ref } };
        });
        if (count === 0) {
            toast.info(t('toast.fillNone'));
            return;
        }
        setDraft(next);
        toast.success(t('toast.fillDone', { count }));
    };

    const handleSave = () => {
        updateListings.mutate(
            { id: channelID, listings: rows },
            {
                onSuccess: () => {
                    toast.success(t('toast.saved'));
                    setDraft(null);
                },
                onError: (error) => toast.error(error.message),
            }
        );
    };

    if (isLoading && rows.length === 0) {
        return <p className="py-8 text-center text-sm text-muted-foreground">{t('loading')}</p>;
    }
    if (rows.length === 0) {
        return <p className="py-8 text-center text-sm text-muted-foreground">{t('empty')}</p>;
    }

    return (
        <div className="flex min-h-0 flex-1 flex-col gap-3">
            <div className="flex flex-wrap items-center gap-2">
                <Input className="h-8 w-40 rounded-xl" placeholder={t('search')} value={keyword}
                    onChange={(event) => setKeyword(event.target.value)} />
                <Select value={filter} onValueChange={(value) => setFilter(value as typeof filter)}>
                    <SelectTrigger className="h-8 w-32 rounded-xl"><SelectValue /></SelectTrigger>
                    <SelectContent>
                        <SelectItem value="all">{t('filter.all')}</SelectItem>
                        <SelectItem value="listed">{t('filter.listed')}</SelectItem>
                        <SelectItem value="unlisted">{t('filter.unlisted')}</SelectItem>
                        <SelectItem value="unpriced">{t('filter.unpriced')}</SelectItem>
                    </SelectContent>
                </Select>
                <span className="text-xs text-muted-foreground">{t('counts', { shown: visible.length, total: rows.length })}</span>
                <div className="flex-1" />
                <Button variant="outline" size="sm" onClick={fillUnpriced} disabled={updateListings.isPending}>
                    <Wand2 className="size-3.5" />
                    {t('fillAll')}
                </Button>
            </div>
            {/* 列数多(上架勾选+模型名+四价+用户价+参考价), 容器窄于 40rem 时整体横向滚动而不是被裁掉后半段; 表头与行共享最小宽度, 滚动对齐一致。 */}
            <div className="min-h-0 flex-1 overflow-auto overscroll-contain md:max-h-80 md:flex-none">
            <div className="min-w-[40rem]">
            <div className="grid grid-cols-[auto_1fr_repeat(4,minmax(0,4.5rem))_minmax(0,7rem)_auto] items-center gap-2 px-1 text-xs text-muted-foreground">
                <span>{t('column.listed')}</span>
                <span>{t('column.model')}</span>
                <span>{t('column.input')}</span>
                <span>{t('column.output')}</span>
                <span>{t('column.cacheRead')}</span>
                <span>{t('column.cacheWrite')}</span>
                <span>{t('column.userPrice')}</span>
                <span>{t('column.fill')}</span>
            </div>
                {visible.length === 0 && (
                    <p className="py-8 text-center text-sm text-muted-foreground">{t('filter.empty')}</p>
                )}
                {visible.map((row) => {
                    // 视图是筛选后的子集, 而草稿是完整清单: 用名称回查真实下标, 避免筛选状态下改错行。
                    const index = rows.findIndex((item) => item.name === row.name);
                    const flagged = row.listed && isUnpriced(row);
                    return (
                    <div key={row.name} className={cn('grid grid-cols-[auto_1fr_repeat(4,minmax(0,4.5rem))_minmax(0,7rem)_auto] items-center gap-2 rounded-xl px-1 py-1.5 hover:bg-muted/50',
                        flagged && 'bg-destructive/5')}>
                        <Switch
                            checked={row.listed}
                            onCheckedChange={(checked) => mutateRow(index, { listed: checked === true })}
                            disabled={updateListings.isPending}
                        />
                        <span className="truncate text-sm font-medium">{row.name}</span>
                        {row.kind === 'image' ? (
                            <>
                                {/* 生图行: 单个每张价输入横跨四列; token 价保持原值随保存提交。 */}
                                <Input
                                    type="number"
                                    min={0}
                                    step="any"
                                    className="h-8 px-2 text-xs col-span-4"
                                    title={t('mediaHint')}
                                    value={row.media_supply?.per_image ?? 0}
                                    disabled={updateListings.isPending}
                                    onChange={(e) => mutateRow(index, {
                                        media_supply: { ...row.media_supply, per_image: Number(e.target.value) || 0 },
                                    })}
                                />
                                <span className="truncate text-xs tabular-nums text-muted-foreground" title={t('userPriceHint')}>
                                    {t('userPricePerImage', { value: formatMoney((row.media_supply?.per_image ?? 0) * (1 + markup)) })}
                                </span>
                            </>
                        ) : (
                            <>
                                {(Object.keys({ input: 0, output: 0, cache_read: 0, cache_write: 0 }) as Array<'input' | 'output' | 'cache_read' | 'cache_write'>).map((field) => (
                                    <Input
                                        key={field}
                                        type="number"
                                        min={0}
                                        step="any"
                                        className="h-8 px-2 text-xs"
                                        value={row.supply_price[field]}
                                        disabled={updateListings.isPending}
                                        onChange={(e) => mutateRow(index, {
                                            supply_price: { ...row.supply_price, [field]: Number(e.target.value) || 0 },
                                        })}
                                    />
                                ))}
                                <span className="truncate text-xs tabular-nums text-muted-foreground" title={t('userPriceHint')}>
                                    {t('userPriceValue', {
                                        input: formatMoney(row.supply_price.input * (1 + markup)),
                                        output: formatMoney(row.supply_price.output * (1 + markup)),
                                    })}
                                </span>
                            </>
                        )}
                        <Button
                            variant="ghost"
                            size="icon"
                            className="size-8"
                            onClick={() => fillRow(index)}
                            disabled={updateListings.isPending}
                            aria-label={t('fillOne')}
                            title={t('fillOne')}
                        >
                            <Wand2 className="size-4" />
                        </Button>
                    </div>
                    );
                })}
            </div>
            </div>
            <p className="text-xs text-muted-foreground">{t('hint')}</p>
            {rows.some((row) => row.kind === 'image') && (
                <p className="text-xs text-muted-foreground">{t('mediaHint')}</p>
            )}
            <p className="text-xs text-muted-foreground">{t('refHint')}</p>
            {zeroPricedListed.length > 0 && (
                <p className="text-xs text-destructive">
                    {t('zeroPriced', { count: zeroPricedListed.length, models: zeroPricedListed.slice(0, 3).join(', ') })}
                </p>
            )}
            <div className="flex items-center justify-end gap-2">
                {changedCount > 0 && (
                    <span className="text-xs text-muted-foreground">{t('changed', { count: changedCount })}</span>
                )}
                <Button onClick={handleSave}
                    disabled={updateListings.isPending || draft === null || changedCount === 0 || zeroPricedListed.length > 0}>
                    {t('save')}
                </Button>
            </div>
        </div>
    );
}

// ShareCard 单个渠道的发布卡片: 发布开关、唯一编码与上架定价入口。
function ShareCard({ channel }: { channel: ChannelStatsFormatted }) {
    const t = useTranslations('share');
    const publishChannel = usePublishChannel();

    const handlePublish = (checked: boolean) => {
        publishChannel.mutate(
            { id: channel.channel_id, shared: checked },
            {
                onSuccess: (result) => toast.success(checked ? t('toast.published', { code: result.share_code }) : t('toast.unpublished')),
                onError: (error) => toast.error(error.message),
            }
        );
    };

    const handleCopy = () => {
        void navigator.clipboard?.writeText(channel.share_code);
        toast.success(t('toast.copied'));
    };

    return (
        <article className="flex flex-col gap-3 rounded-3xl border border-border bg-card text-card-foreground p-4">
            <header className="flex items-center justify-between gap-2">
                <h3 className="truncate text-lg font-bold">{channel.channel_name}</h3>
                <Switch
                    checked={channel.shared}
                    onCheckedChange={handlePublish}
                    disabled={publishChannel.isPending}
                />
            </header>

            <div className="rounded-2xl border border-border/70 bg-background/80 p-3">
                <p className="text-xs text-muted-foreground">{t('shareCode')}</p>
                {channel.shared && channel.share_code ? (
                    <div className="mt-1 flex items-center gap-2">
                        <code className="min-w-0 flex-1 truncate font-mono text-sm font-semibold">{channel.share_code}</code>
                        <Button variant="ghost" size="icon" onClick={handleCopy} aria-label={t('copyCode')}>
                            <Copy className="size-4" />
                        </Button>
                    </div>
                ) : (
                    <p className="mt-1 text-sm text-muted-foreground">{t('notPublished')}</p>
                )}
            </div>

            <MorphingDialog>
                <MorphingDialogTrigger className="w-full">
                    <Button variant="outline" className="w-full">
                        <PackageCheck className="size-4" />
                        {t('manageListing')}
                    </Button>
                </MorphingDialogTrigger>
                <MorphingDialogContainer>
                    {/* 移动端弹窗必须自带滚动: 内容(工具栏+模型清单+底部按钮)高于视口时,
                        外层虽可滚但内容被 overflow-hidden 与 h-fit 夹住, 表现为只能看到顶部一截且拉不动。
                        max-h 用 dvh 卡住高度, 内部再自己滚, 底部按钮才始终够得着。
                        iPad/桌面按可用宽度自适应: 窄屏全宽, 大屏逐级放宽; min-h 固定 rem 兜底,
                        即使动画或 dvh 失效也不至于塌成小条。
                        注意: JSX 子节点位置只能用花括号注释, 行注释会被当成文本渲染并挤压布局。 */}
                    <MorphingDialogContent className="flex h-fit min-h-[24rem] max-h-[calc(100dvh-2rem)] w-full flex-col overflow-hidden rounded-3xl bg-card p-4 text-card-foreground md:max-w-2xl lg:max-w-3xl xl:max-w-4xl">
                        <MorphingDialogDescription className="relative flex min-h-0 flex-1 flex-col">
                            <h4 className="mb-3 text-lg font-bold">{t('listing.title', { name: channel.channel_name })}</h4>
                            <ListingRows channelID={channel.channel_id} />
                        </MorphingDialogDescription>
                    </MorphingDialogContent>
                </MorphingDialogContainer>
            </MorphingDialog>
        </article>
    );
}

// Share 渲染发布管理正文: 渠道商发布自己的渠道, 管理员可发布任意渠道。
export default function Share() {
    const t = useTranslations('share');
    const { data: statsData, isLoading, isError } = useChannelStats();

    const channels = useMemo(() => statsData ?? [], [statsData]);

    if (isError) {
        return <p className="py-12 text-center text-sm text-muted-foreground">{t('loadFailed')}</p>;
    }
    if (isLoading) {
        return <p className="py-12 text-center text-sm text-muted-foreground">{t('loading')}</p>;
    }
    if (channels.length === 0) {
        return <p className="py-12 text-center text-sm text-muted-foreground">{t('empty')}</p>;
    }

    return (
        // h-full + overflow-y-auto: 页面切换层是 absolute inset-0, 外层不会跟着内容长高,
        // 因此这一层必须自己滚, 否则内容超出视口后拖不动(手机上只能看到顶部一屏)。
        <div className="grid h-full grid-cols-1 content-start gap-4 overflow-y-auto overscroll-contain pb-24 sm:grid-cols-2 lg:grid-cols-3">
            {channels.map((channel) => (
                <ShareCard key={channel.channel_id} channel={channel} />
            ))}
        </div>
    );
}
