import { useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Database, RefreshCw } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { useSetSetting, useSettingList, SettingKey } from '@/api/setting';
import { useBillingLifecycle, useRunBillingLifecycle } from '@/api/ops';
import { formatTableCount } from './billingCounts';

// 资金表留存配置卡片。
// 与「审计归档」分开成一张卡的三个理由:
//   1) 影响的表完全不同(billing_records / balance_reservations / balance_ledgers /
//      billing_settlement_tasks), 合成一张会让人以为改一个值两边都变;
//   2) 这里的清理会**不可逆地删除资金明细**, 需要在界面上说清楚"归档先行、删前固化汇总"
//      这套保护, 而不是让人以为只是删日志;
//   3) 四张表各自的现有行数要能看见 —— 只给一个天数档位, 管理员根本判断不出清理有没有效果。

// formatBytes 文件大小人读格式(与审计卡片同一口径)。
function formatBytes(size: number): string {
    if (!size) return '0 B';
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    let value = size;
    let unit = 0;
    while (value >= 1024 && unit < units.length - 1) {
        value /= 1024;
        unit += 1;
    }
    return (unit === 0 ? value : value.toFixed(1)) + ' ' + units[unit];
}

// formatTime 把后端时间转成本地展示; 非法值原样返回, 不显示 "Invalid Date"。
function formatTime(value: string): string {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}



export function SettingBilling() {
    const t = useTranslations('setting.billing');
    const tRoot = useTranslations('setting');
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();
    const lifecycle = useBillingLifecycle();
    const run = useRunBillingLifecycle();

    // 已保存值派生; 本地只存草稿(与审计/运维卡片同一套"服务端是权威"的写法)。
    const saved = useMemo(() => {
        const days = settings?.find((item) => item.key === SettingKey.BillingRetentionDays)?.value ?? '';
        const enabled = settings?.find((item) => item.key === SettingKey.BillingArchiveEnabled)?.value ?? '';
        return { days, enabled };
    }, [settings]);
    const [daysDraft, setDaysDraft] = useState<string | null>(null);
    const [enabledDraft, setEnabledDraft] = useState<string | null>(null);

    // 显示优先级: 本地草稿 > 设置值 > 接口回显的生效值 > 后端默认 180。
    // 空串不是 nullish, 用 ?? 会把"键从未写入"当成有效值而停在空白。
    const daysValue = daysDraft
        ?? (saved.days !== ''
            ? saved.days
            : lifecycle.data
                ? String(lifecycle.data.retention_days)
                : '180');
    const enabledValue = enabledDraft !== null
        ? enabledDraft === 'true'
        : (saved.enabled !== '' ? saved.enabled === 'true' : (lifecycle.data?.archive_enabled ?? true));

    // saveDays 失焦时夹取到 1-3650 再保存; 非法输入回落到后端默认 180。
    // 数字框必须 onChange 只写草稿、失焦才提交: 受控输入缺 onChange 时按键会被 React 回滚。
    const saveDays = (raw: string) => {
        const parsed = Number.parseInt(raw, 10);
        const normalized = Number.isFinite(parsed) ? String(Math.min(Math.max(parsed, 1), 3650)) : '180';
        setDaysDraft(normalized);
        if (saved.days === normalized) return;
        setSetting.mutate({ key: SettingKey.BillingRetentionDays, value: normalized }, {
            onSuccess: () => toast.success(tRoot('saved')),
            onError: (error) => {
                setDaysDraft(saved.days || null);
                toast.error(error instanceof Error ? error.message : tRoot('saveFailed'));
            },
        });
    };

    // saveEnabled 立即保存归档开关; 草稿同步更新以便开关立刻响应。
    const saveEnabled = (checked: boolean) => {
        const value = String(checked);
        setEnabledDraft(value);
        if (saved.enabled === value) return;
        setSetting.mutate({ key: SettingKey.BillingArchiveEnabled, value }, {
            onSuccess: () => toast.success(tRoot('saved')),
            onError: (error) => {
                setEnabledDraft(null);
                toast.error(error instanceof Error ? error.message : tRoot('saveFailed'));
            },
        });
    };

    // runNow 立即清理: 后端要 step-up(删资金明细不可逆), apiRequest 会自动弹密码确认并重发。
    const runNow = () => {
        run.mutate(undefined, {
            onSuccess: (result) => {
                if (result.archive_error) {
                    toast.error(t('archiveFailed', { error: result.archive_error }));
                    return;
                }
                toast.success(t('runSuccess', {
                    archived: result.archived,
                    records: result.deleted_records,
                    ledgers: result.deleted_ledgers,
                }));
            },
            onError: (error) => toast.error(error instanceof Error ? error.message : t('runFailed')),
        });
    };

    const files = lifecycle.data?.items ?? [];
    // 四张表 + 汇总表的现有行数; 后端按表名给。
    // 这里**不做 ?? {} 之外的兜底**: 缺某个键就让 formatCount 显「—」,
    // 而不是补一个 0 —— 一张表的真实行数是 5 万还是"这次没读到", 决定了管理员要不要点「立即清理」。
    const counts = lifecycle.data?.counts ?? {};
    const countRows: { key: string; label: string }[] = [
        { key: 'billing_records', label: t('tableRecords') },
        { key: 'balance_ledgers', label: t('tableLedgers') },
        { key: 'balance_reservations', label: t('tableReservations') },
        { key: 'billing_settlement_tasks', label: t('tableTasks') },
        { key: 'billing_rollups', label: t('tableRollups') },
    ];

    return (
        <div className="space-y-4 rounded-3xl border border-border bg-card p-6">
            <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                <Database className="size-5" aria-hidden="true" />
                {t('title')}
            </h2>
            <p className="text-xs text-muted-foreground">{t('intro')}</p>

            <div className="flex flex-wrap items-center justify-between gap-3">
                <div className="min-w-0">
                    <Label htmlFor="billing-retention-days" className="text-sm font-medium">{t('retentionDays')}</Label>
                    <p className="text-xs text-muted-foreground">{t('retentionDaysHint')}</p>
                </div>
                <Input
                    id="billing-retention-days"
                    type="number"
                    min={1}
                    max={3650}
                    className="h-9 w-28 rounded-xl"
                    value={daysValue}
                    onChange={(event) => setDaysDraft(event.target.value)}
                    onBlur={(event) => saveDays(event.target.value.trim())}
                />
            </div>

            <div className="flex flex-wrap items-center justify-between gap-3 border-t border-border/60 pt-3">
                <div className="min-w-0">
                    <Label className="text-sm font-medium">{t('archiveEnabled')}</Label>
                    <p className="text-xs text-muted-foreground">{t('archiveEnabledHint')}</p>
                </div>
                <Switch checked={enabledValue} onCheckedChange={saveEnabled} aria-label={t('archiveEnabled')} />
            </div>

            <div className="space-y-2 border-t border-border/60 pt-3">
                <div className="min-w-0">
                    <Label className="text-sm font-medium">{t('countsTitle')}</Label>
                    <p className="text-xs text-muted-foreground">{t('countsHint')}</p>
                </div>
                {/* 取数失败时整列「—」并顶一条说明: 只把数字换成 — 而不解释, 会被读成"数据还在算"。 */}
                {lifecycle.isError && (
                    <p role="alert" className="rounded-2xl border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive">
                        {t('countsFailed')}
                    </p>
                )}
                <div className="overflow-x-auto rounded-2xl border border-border">
                    <table className="w-full min-w-[20rem] text-sm">
                        <tbody>
                            {countRows.map((row) => (
                                <tr key={row.key} className="border-b border-border/60 last:border-0">
                                    <td className="px-4 py-2 text-muted-foreground">{row.label}</td>
                                    <td className="px-4 py-2 text-right font-mono tabular-nums">{formatTableCount(counts[row.key])}</td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>
            </div>

            <div className="space-y-2 border-t border-border/60 pt-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="min-w-0">
                        <Label className="text-sm font-medium">{t('filesTitle')}</Label>
                        <p className="text-xs text-muted-foreground">{t('filesHint')}</p>
                    </div>
                    <div className="flex items-center gap-2">
                        <Button size="sm" className="rounded-xl" onClick={runNow} disabled={run.isPending}>
                            {run.isPending ? t('running') : t('runNow')}
                        </Button>
                        <Button
                            variant="outline"
                            size="sm"
                            className="rounded-xl"
                            onClick={() => void lifecycle.refetch()}
                            disabled={lifecycle.isRefetching}
                            aria-label={t('refresh')}
                        >
                            <RefreshCw className={'size-4' + (lifecycle.isRefetching ? ' animate-spin' : '')} aria-hidden="true" />
                            {t('refresh')}
                        </Button>
                    </div>
                </div>

                {lifecycle.isError ? (
                    <p className="rounded-2xl border border-border bg-muted/40 px-4 py-6 text-center text-sm text-muted-foreground">
                        {t('loadFailed')}
                    </p>
                ) : files.length === 0 ? (
                    <p className="rounded-2xl border border-border bg-muted/40 px-4 py-6 text-center text-sm text-muted-foreground">
                        {lifecycle.isLoading ? t('loading') : t('empty')}
                    </p>
                ) : (
                    <div className="overflow-x-auto rounded-2xl border border-border">
                        <table className="w-full min-w-[28rem] text-sm">
                            <thead className="text-left text-xs text-muted-foreground">
                                <tr className="border-b border-border">
                                    <th className="px-4 py-2 font-medium">{t('colName')}</th>
                                    <th className="px-4 py-2 font-medium">{t('colSize')}</th>
                                    <th className="px-4 py-2 font-medium">{t('colTime')}</th>
                                </tr>
                            </thead>
                            <tbody>
                                {files.map((file, index) => (
                                    <tr key={file.name || index} className="border-b border-border/60 last:border-0">
                                        <td className="max-w-[18rem] truncate px-4 py-2 font-mono text-xs" title={file.name}>{file.name || '\u2014'}</td>
                                        <td className="px-4 py-2 tabular-nums">{formatBytes(file.size)}</td>
                                        <td className="px-4 py-2 tabular-nums text-muted-foreground">{formatTime(file.mod_time)}</td>
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
