import { CircleAlert } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { Button } from '@/components/ui/button';

/**
 * ListErrorState 列表加载失败时的占位。
 *
 * 为什么需要它: 列表页此前只解构 `data`, 不看 `isError`。请求失败时 data 是 undefined →
 * 列表为空 → VirtualizedGrid 在 rowCount === 0 时直接返回 null → **整页纯空白**。
 * 空态文案同样不会出现, 于是用户看到的是"什么都没有", 会把一次接口故障理解成
 * "数据全丢了"。这里把失败说清楚, 并给出重试入口。
 */
export function ListErrorState({ onRetry, retrying }: { onRetry?: () => void; retrying?: boolean }) {
    const t = useTranslations('common');
    return (
        <div className="flex h-full min-h-40 flex-col items-center justify-center gap-3 text-muted-foreground">
            <CircleAlert className="size-8" />
            <p className="text-sm">{t('loadFailed')}</p>
            {onRetry && (
                <Button variant="outline" size="sm" className="rounded-xl" onClick={onRetry} disabled={retrying}>
                    {t('retry')}
                </Button>
            )}
        </div>
    );
}
