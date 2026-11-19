import { useState } from 'react';
import { ScrollText, Trash2 } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { useClearLogs } from '@/api/log';
import { Button } from '@/components/ui/button';
import { toast } from 'sonner';

// SettingLog 提供进程内完成日志的清空操作。
export function SettingLog() {
    const t = useTranslations('setting');
    const clearLogs = useClearLogs();

    // 清空日志不可撤销, 而按钮就在设置页上、原本单击即生效。
    // 先就地展开确认, 把"清空之后就没有了"写在按钮旁边。
    const [confirming, setConfirming] = useState(false);
    const handleClearLogs = () => {
        clearLogs.mutate(undefined, {
            onSuccess: () => {
                setConfirming(false);
                toast.success(t('log.clearSuccess'));
            },
            onError: () => {
                setConfirming(false);
                toast.error(t('log.clearFailed'));
            },
        });
    };

    return (
        <div className="space-y-5 rounded-3xl border border-border bg-card p-6">
            <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                <ScrollText className="size-5" />
                {t('log.title')}
            </h2>
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Trash2 className="size-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('log.clear.label')}</span>
                </div>
                {confirming ? (
                    <div className="flex items-center gap-1.5">
                        <span className="text-xs text-muted-foreground">{t('log.clear.hint')}</span>
                        <Button variant="destructive" size="sm" onClick={handleClearLogs} disabled={clearLogs.isPending} className="rounded-xl">
                            {clearLogs.isPending ? t('log.clear.clearing') : t('log.clear.confirm')}
                        </Button>
                        <Button variant="outline" size="sm" onClick={() => setConfirming(false)} className="rounded-xl">{t('log.clear.cancel')}</Button>
                    </div>
                ) : (
                    <Button
                        variant="destructive"
                        size="sm"
                        onClick={() => setConfirming(true)}
                        disabled={clearLogs.isPending}
                        className="rounded-xl"
                    >
                        {clearLogs.isPending ? t('log.clear.clearing') : t('log.clear.button')}
                    </Button>
                )}
            </div>
        </div>
    );
}
