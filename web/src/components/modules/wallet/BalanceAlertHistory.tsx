import { useTranslations } from 'use-intl';
import { Loader2 } from 'lucide-react';
import { useBalanceAlertHistory } from '@/api/balance-alert';
import { Button } from '@/components/ui/button';
import { formatDateTime } from '@/lib/format';

export function BalanceAlertHistory() {
    const t = useTranslations('wallet.alert');
    const history = useBalanceAlertHistory();
    return (
        <section className="shrink-0 rounded-3xl border border-border bg-card px-4 py-3" aria-labelledby="balance-alert-history-title">
            <h3 id="balance-alert-history-title" className="text-sm font-medium">{t('historyTitle')}</h3>
            {history.isLoading ? (
                <div role="status" className="flex items-center gap-2 py-3 text-sm text-muted-foreground"><Loader2 className="size-4 animate-spin" />{t('historyLoading')}</div>
            ) : history.isError ? (
                <div role="alert" className="flex items-center gap-2 py-3 text-sm text-destructive">
                    <span>{t('historyError')}</span>
                    <Button variant="outline" size="sm" onClick={() => void history.refetch()} disabled={history.isFetching}>{t('historyRetry')}</Button>
                </div>
            ) : !history.data?.items.length ? (
                <p className="py-3 text-sm text-muted-foreground">{t('historyEmpty')}</p>
            ) : (
                <div className="max-h-64 overflow-auto">
                    <table className="w-full text-sm">
                        <thead><tr className="text-left text-muted-foreground"><th className="py-2 font-normal">{t('historyTime')}</th><th className="py-2 font-normal">{t('historySource')}</th></tr></thead>
                        <tbody>{history.data.items.map((row, index) => (
                            <tr key={row.triggered_at + index} className="border-t border-border"><td className="py-2 tabular-nums"><time dateTime={row.triggered_at}>{formatDateTime(row.triggered_at, '—')}</time></td><td className="py-2">{row.source}</td></tr>
                        ))}</tbody>
                    </table>
                </div>
            )}
        </section>
    );
}
