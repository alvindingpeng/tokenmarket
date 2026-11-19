import { TriangleAlert } from 'lucide-react';
import { toast } from 'sonner';
import { useTranslations } from 'use-intl';
import { type ChannelStatsFormatted, useResetChannelBreaker } from '@/api/channel';
import { isBroken, remainText } from './breaker';
import { channelPauseResetVariables } from './pause';

// ChannelPauseBadge 展示渠道级的"额度暂停"并给出一键恢复。
// 它与人工启停开关、模型级熔断是三件互不相干的事: 上游余额不足触发整渠道暂停, 到期自动恢复;
// 不看这一条, 管理员只会看到整条渠道的请求全失败, 却从列表里找不到原因。
// 未暂停时整块不渲染(而不是渲染成灰态), 否则每张卡片都挂一个"未暂停"的噪音。
export function ChannelPauseBadge({
    channel,
    hint = false,
}: {
    channel: ChannelStatsFormatted;
    // hint 为真时额外给一行说明, 用在详情里; 列表卡片位置紧张, 说明改由 title 承载。
    hint?: boolean;
}) {
    const t = useTranslations('channel.stats');
    const resetBreaker = useResetChannelBreaker();

    if (!isBroken(channel.disabledUntil)) return null;

    const remain = remainText(channel.disabledUntil);

    return (
        <div data-share-exclude className="flex flex-wrap items-center gap-2" title={t('pauseHint')}>
            <span className="flex shrink-0 items-center gap-1 rounded-md bg-destructive/10 px-2 py-1 text-xs font-semibold text-destructive">
                <TriangleAlert className="size-3.5" aria-hidden="true" />
                {t('pauseBadge', { time: remain })}
            </span>
            <button
                type="button"
                disabled={resetBreaker.isPending}
                onClick={(event) => {
                    // 卡片本身是"打开详情"的触发器, 点恢复不能顺手把弹窗也打开。
                    event.stopPropagation();
                    resetBreaker.mutate(
                        channelPauseResetVariables(channel),
                        {
                            onSuccess: () => toast.success(t('pauseReset', { name: channel.channel_name })),
                            onError: (error) => toast.error(error instanceof Error ? error.message : String(error)),
                        },
                    );
                }}
                className="shrink-0 rounded-md border border-destructive/40 px-2 py-1 text-xs font-medium text-destructive transition-colors hover:bg-destructive/10 disabled:opacity-50"
            >
                {t('pauseResume')}
            </button>
            {hint && <span className="text-[11px] leading-snug text-muted-foreground">{t('pauseHint')}</span>}
        </div>
    );
}
