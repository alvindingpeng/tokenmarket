import { useTranslations } from 'use-intl';
import { AlertTriangle, CircleCheck, CircleHelp, CircleSlash } from 'lucide-react';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import type { ChannelKey } from '@/api/channel';
import { accountStateOf, remainingOf, type AccountState } from './accountState';

// AccountBadge 把一条账号凭据的状态画成"图标 + 文字"。
//
// 为什么不是一个小圆点: 圆点要靠颜色区分, 而颜色对色觉障碍与深色主题都不友好;
// 图标 + 文字在两种主题下都能读, 也不需要使用者先学会一套图例。
//
// 四种状态各自的图标是有意选的: 对勾=能用, 三角=快到期, 问号=要人工, 斜杠=不能用。
// "不能用"和"要人工"必须区分开 —— 前者是系统判定失效, 后者是这类账号本就没有自动续期能力,
// 混成一个图标会让人以为 cookie 账号出了故障。

const ICONS: Record<AccountState, typeof CircleCheck> = {
    healthy: CircleCheck,
    expiring: AlertTriangle,
    manual: CircleHelp,
    invalid: CircleSlash,
};

const TONES: Record<AccountState, string> = {
    healthy: 'text-emerald-600 dark:text-emerald-500',
    expiring: 'text-amber-600 dark:text-amber-500',
    invalid: 'text-destructive',
    manual: 'text-muted-foreground',
};

export function AccountStateBadge({ channelKey, now }: { channelKey: ChannelKey; now?: number }) {
    const t = useTranslations('channel.account.state');
    const state = accountStateOf(channelKey, now);
    const Icon = ICONS[state];
    const remaining = remainingOf(channelKey.token_expires_at, now);

    // 正常状态下只显示图标与"正常": 剩余几十天写出来没有信息量, 反而占地方。
    // 临期与失效才需要具体数字 —— 那时"还剩多久"正是使用者要决定做什么的依据。
    const detail = state === 'expiring' && remaining && 'minutes' in remaining
        ? t('remainingMinutes', { count: remaining.minutes })
        : state === 'expiring' && remaining && 'hours' in remaining
            ? t('remainingHours', { count: remaining.hours })
            : state === 'expiring' && remaining && 'days' in remaining
                ? t('remainingDays', { count: remaining.days })
                : null;

    return (
        <Tooltip>
            <TooltipTrigger asChild>
                <span className={`inline-flex shrink-0 items-center gap-1 text-xs ${TONES[state]}`}>
                    <Icon className="size-3.5" />
                    {detail ?? t(state)}
                </span>
            </TooltipTrigger>
            <TooltipContent side="top" className="max-w-64 text-xs">
                <p className="font-medium">{t(state)}</p>
                <p className="mt-1 text-muted-foreground">{t(`hint.${state}`)}</p>
                {channelKey.token_refresh_at && (
                    <p className="mt-1 text-muted-foreground">
                        {t('lastRefresh', { at: channelKey.token_refresh_at })}
                    </p>
                )}
            </TooltipContent>
        </Tooltip>
    );
}

// AccountIdentity 显示"哪个平台的账号、邮箱是谁", 替代原来那串掩码 JSON。
//
// 掩码 JSON 对账号凭据毫无信息量: 它的前三位是 '{"' 后四位是随机的字母,
// 使用者既认不出是哪个账号, 也判断不了它是否还有效。
export function AccountIdentity({ channelKey }: { channelKey: ChannelKey }) {
    const platform = channelKey.account_platform || channelKey.account_kind;
    return (
        <span className="flex min-w-0 items-center gap-1.5">
            <span className="shrink-0 rounded-md bg-muted px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-wide text-muted-foreground">
                {platform}
            </span>
            {channelKey.account_email && (
                <span className="truncate text-xs text-muted-foreground">{channelKey.account_email}</span>
            )}
        </span>
    );
}
