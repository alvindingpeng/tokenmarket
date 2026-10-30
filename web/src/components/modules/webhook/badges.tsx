import { useTranslations } from 'use-intl';
import { Badge } from '@/components/ui/badge';
import { isWebhookEvent } from '@/api/webhook';
import { notificationTypeKey } from '@/api/notification';

// 订阅/投递的徽标: 颜色只是辅助, 文案才是主语义(色弱用户不靠颜色区分状态)。
// 色板沿用 tickets 模块的既有 token 口径(bg-*-500/10 + dark 文本), 不引入新色。

// EnabledBadge 订阅启停状态。
export function EnabledBadge({ enabled }: { enabled: boolean }) {
    const t = useTranslations('webhook');
    return (
        <Badge variant="outline" className={enabled
            ? 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'
            : 'bg-muted text-muted-foreground'}>
            {t(enabled ? 'enabledBadge.on' : 'enabledBadge.off')}
        </Badge>
    );
}

// DeliveryStatusBadge 投递状态: 认识的状态词给三语文案, 认不出的后端新状态原样展示 ——
// 硬拼文案键会在 use-intl 缺键时抛错整页白屏, 宁可显示原文。
// 状态词与 model.WebhookDeliveryStatus 对齐: pending/processing/succeeded/failed/cancelled;
// none(从未投递)与空值一样按「无记录」显示占位符。
export function DeliveryStatusBadge({ status }: { status?: string | null }) {
    const t = useTranslations('webhook');
    if (!status || status === 'none') return <span className="text-muted-foreground">{'\u2014'}</span>;
    const known = status === 'pending' || status === 'processing' || status === 'succeeded' || status === 'failed' || status === 'cancelled';
    const className = status === 'succeeded'
        ? 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'
        : status === 'failed'
            ? 'bg-red-500/10 text-red-600 dark:text-red-400'
            : status === 'pending' || status === 'processing'
                ? 'bg-amber-500/10 text-amber-600 dark:text-amber-400'
                : 'bg-muted text-muted-foreground';
    return (
        <Badge variant="outline" className={className}>
            {known ? t('delivery.' + status) : status}
        </Badge>
    );
}

// EventBadge 事件名: 已知事件用通知类型的三语文案, 未知事件名原样展示。
export function EventBadge({ event }: { event: string }) {
    const t = useTranslations('notification');
    return (
        <Badge variant="outline" className="text-muted-foreground">
            {isWebhookEvent(event) ? t(notificationTypeKey(event)) : event}
        </Badge>
    );
}
