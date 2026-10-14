import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { AlertCircle, Coins, Loader2, RefreshCw, Ticket } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { useWalletLedger, useWalletSummary } from '@/api/wallet';
import { useRedeem } from '@/api/redemption';
import { useAuthStore } from '@/api/user';

// money 金额千分位展示; 余额是 6 位小数口径, 展示截到 4 位可读精度。
function money(value: number): string {
    return Number(value || 0).toLocaleString(undefined, { maximumFractionDigits: 4 });
}

// formatTime 将后端 RFC3339 时间格式化为本地时间; 非法值原样展示。
function formatTime(value: string): string {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

// KINDS 流水筛选项; 空串表示全部。
const KINDS = ['', 'topup', 'redemption', 'reserve', 'settle', 'release', 'adjust'];

// Wallet 钱包页: 余额卡片 + 兑换码 + 余额流水; 充值与提现入口在后续阶段接入同一页面。
export default function Wallet() {
    const t = useTranslations('wallet');
    const role = useAuthStore((state) => state.role);
    const uid = useAuthStore((state) => state.userId);
    const [kind, setKind] = useState('');
    const [code, setCode] = useState('');
    const summary = useWalletSummary();
    const ledger = useWalletLedger(kind, role === 'admin' ? uid : 0);
    const redeem = useRedeem();

    const items = ledger.data?.items ?? [];
    const cards = [
        { key: 'balance', value: summary.data?.balance },
        { key: 'frozen', value: summary.data?.frozen },
        { key: 'recharged', value: summary.data?.total_recharged },
        { key: 'spent', value: summary.data?.total_spent },
        { key: 'revenue', value: summary.data?.total_revenue },
    ] as const;

    // handleRedeem 兑换并即时回执到账额与新余额; 失败原因透出(错码/已兑/锁定)。
    const handleRedeem = () => {
        const value = code.trim();
        if (value === '' || redeem.isPending) return;
        redeem.mutate(value, {
            onSuccess: (data) => {
                toast.success(t('redeem.ok', { credits: money(data.credits), balance: money(data.balance) }));
                setCode('');
            },
            onError: (error) => toast.error(t('redeem.failed'), { description: error.message }),
        });
    };

    return (
        <div className="flex h-full min-h-0 flex-col gap-3 pb-24 md:pb-0">
            <div className="flex shrink-0 flex-wrap items-center justify-between gap-2">
                <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                    <Coins className="size-5" />
                    {t('title')}
                </h2>
                <Button variant="outline" size="sm" onClick={() => { void summary.refetch(); void ledger.refetch(); }} disabled={summary.isRefetching} className="rounded-xl">
                    <RefreshCw className={'size-4' + (summary.isRefetching ? ' animate-spin' : '')} />
                    {t('refresh')}
                </Button>
            </div>

            <div className="grid shrink-0 grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-5">
                {cards.map((card) => (
                    <div key={card.key} className="rounded-3xl border border-border bg-card px-4 py-3">
                        <div className="text-xs text-muted-foreground">{t('card.' + card.key)}</div>
                        <div className="mt-1 truncate text-xl font-semibold tabular-nums" title={money(card.value ?? 0)}>
                            {money(card.value ?? 0)}
                        </div>
                    </div>
                ))}
            </div>

            <div className="flex shrink-0 flex-wrap items-center gap-2 rounded-3xl border border-border bg-card px-4 py-3">
                <Ticket className="size-5 shrink-0 text-primary" />
                <div className="min-w-0 flex-1 basis-52">
                    <div className="text-sm font-medium">{t('redeem.title')}</div>
                    <div className="text-xs text-muted-foreground">{t('redeem.desc')}</div>
                </div>
                <Input
                    className="h-9 w-full rounded-xl sm:w-64"
                    placeholder={t('redeem.placeholder')}
                    value={code}
                    onChange={(event) => setCode(event.target.value)}
                    onKeyDown={(event) => { if (event.key === 'Enter') handleRedeem(); }}
                    autoComplete="off"
                />
                <Button size="sm" className="h-9 rounded-xl" onClick={handleRedeem} disabled={code.trim() === '' || redeem.isPending}>
                    {redeem.isPending && <Loader2 className="size-4 animate-spin" />}
                    {t('redeem.submit')}
                </Button>
            </div>

            <div className="flex shrink-0 flex-wrap items-center gap-2">
                <div className="text-sm font-medium text-card-foreground">{t('ledger.title')}</div>
                <Select value={kind || 'all'} onValueChange={(value) => setKind(value === 'all' ? '' : value)}>
                    <SelectTrigger className="w-40 rounded-xl"><SelectValue placeholder={t('ledger.kindAll')} /></SelectTrigger>
                    <SelectContent>
                        {KINDS.map((value) => (
                            <SelectItem key={value} value={value || 'all'}>{value === '' ? t('ledger.kindAll') : t('kind.' + value)}</SelectItem>
                        ))}
                    </SelectContent>
                </Select>
            </div>

            <div className="min-h-0 flex-1 overflow-auto overscroll-contain rounded-3xl border border-border bg-card">
                {ledger.isLoading ? (
                    <div className="flex h-full items-center justify-center gap-2 text-muted-foreground"><Loader2 className="size-4 animate-spin" />{t('loading')}</div>
                ) : ledger.isError ? (
                    <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground"><AlertCircle className="size-8" /><p>{t('error')}</p><Button variant="outline" size="sm" onClick={() => void ledger.refetch()} className="rounded-xl">{t('retry')}</Button></div>
                ) : items.length === 0 ? (
                    <div className="flex h-full items-center justify-center text-muted-foreground">{t('empty')}</div>
                ) : (
                    <div className="overflow-x-auto">
                        <table className="w-full min-w-[640px] text-sm">
                            <thead className="sticky top-0 bg-card text-left text-xs text-muted-foreground"><tr>
                                <th className="px-4 py-3 font-medium">{t('table.time')}</th>
                                <th className="px-4 py-3 font-medium">{t('table.kind')}</th>
                                <th className="px-4 py-3 font-medium">{t('table.amount')}</th>
                                <th className="px-4 py-3 font-medium">{t('table.balance')}</th>
                                <th className="px-4 py-3 font-medium">{t('table.note')}</th>
                            </tr></thead>
                            <tbody>
                                {items.map((item) => (
                                    <tr key={item.id} className="border-t border-border">
                                        <td className="px-4 py-3 tabular-nums text-muted-foreground">{formatTime(item.created_at)}</td>
                                        <td className="px-4 py-3">{t('kind.' + item.kind)}</td>
                                        <td className={'px-4 py-3 tabular-nums font-medium ' + (item.amount >= 0 ? 'text-emerald-500' : 'text-destructive')}>{money(item.amount)}</td>
                                        <td className="px-4 py-3 tabular-nums">{money(item.balance_after)}</td>
                                        <td className="px-4 py-3 text-muted-foreground">{item.note || (item.model_name ? item.model_name : '-')}</td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                    </div>
                )}
            </div>
        </div>
    );
}
