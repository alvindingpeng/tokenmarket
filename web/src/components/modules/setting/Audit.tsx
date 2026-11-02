import { useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Archive, RefreshCw } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { useSetSetting, useSettingList, SettingKey } from '@/api/setting';
import { useAuditArchives, useRunAuditLifecycle } from '@/api/ops';

// 审计归档配置卡片。
// 与「调用日志归档」分开的原因是留存期语义完全不同: 审计是合规证据(默认 180 天),
// 调用日志只保留数天。共用一张卡片会让人误以为改一个值两边都变。
//
// 三个数据源各自独立:
//   1) 两个设置键(audit_retention_days / audit_archive_enabled)走设置页统一接口;
//   2) 归档文件清单来自 /ops/audit-lifecycle/list(只读);
//   3) 「立即归档」调 /ops/audit-lifecycle/run, 后端要求 step-up, apiRequest 会自动弹密码确认并重发。

// formatBytes 文件大小人读格式(与运维页同一口径, 但接受后端可能给出的 0/缺省)。
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

export function SettingAudit() {
    const t = useTranslations('setting.audit');
    const tRoot = useTranslations('setting');
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();
    const archives = useAuditArchives();
    const run = useRunAuditLifecycle();

    // 已保存值派生; 本地只存草稿(与运维可靠性卡片同一套"服务端是权威"的写法)。
    const saved = useMemo(() => {
        const days = settings?.find((item) => item.key === SettingKey.AuditRetentionDays)?.value ?? '';
        const enabled = settings?.find((item) => item.key === SettingKey.AuditArchiveEnabled)?.value ?? '';
        return { days, enabled };
    }, [settings]);
    const [daysDraft, setDaysDraft] = useState<string | null>(null);
    const [enabledDraft, setEnabledDraft] = useState<string | null>(null);

    // 显示优先级: 本地草稿 > 设置值 > 接口回显的生效值 > 后端默认 180。
    // 注意设置值是空串(键从未写入)时要继续往下回落 —— 空串不是 nullish,
    // 用 ?? 会把它当成有效值, 输入框就停在空白而不是显示生效中的天数。
    const daysValue = daysDraft
        ?? (saved.days !== ''
            ? saved.days
            : archives.data
                ? String(archives.data.retention_days)
                : '180');
    const enabledValue = enabledDraft !== null
        ? enabledDraft === 'true'
        : (saved.enabled !== '' ? saved.enabled === 'true' : (archives.data?.enabled ?? true));

    // saveDays 失焦时夹取到 1-3650 再保存; 非法输入回落到后端默认 180。
    // 数字框必须 onChange 只写草稿、失焦才提交: 受控输入缺 onChange 时按键会被 React 回滚。
    const saveDays = (raw: string) => {
        const parsed = Number.parseInt(raw, 10);
        const normalized = Number.isFinite(parsed) ? String(Math.min(Math.max(parsed, 1), 3650)) : '180';
        setDaysDraft(normalized);
        if (saved.days === normalized) return;
        setSetting.mutate({ key: SettingKey.AuditRetentionDays, value: normalized }, {
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
        setSetting.mutate({ key: SettingKey.AuditArchiveEnabled, value }, {
            onSuccess: () => toast.success(tRoot('saved')),
            onError: (error) => {
                setEnabledDraft(null);
                toast.error(error instanceof Error ? error.message : tRoot('saveFailed'));
            },
        });
    };

    // runNow 立即归档: 成功后同时失效归档清单与设置(留存/开关的回显可能随执行更新)。
    const runNow = () => {
        run.mutate(undefined, {
            onSuccess: (result) => toast.success(t('runSuccess', { archived: result.archived, deleted: result.deleted })),
            onError: () => toast.error(t('runFailed')),
        });
    };

    const files = archives.data?.items ?? [];

    return (
        <div className="space-y-4 rounded-3xl border border-border bg-card p-6">
            <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                <Archive className="size-5" aria-hidden="true" />
                {t('title')}
            </h2>

            <div className="flex flex-wrap items-center justify-between gap-3">
                <div className="min-w-0">
                    <Label htmlFor="audit-retention-days" className="text-sm font-medium">{t('retentionDays')}</Label>
                    <p className="text-xs text-muted-foreground">{t('retentionDaysHint')}</p>
                </div>
                <Input
                    id="audit-retention-days"
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
                            onClick={() => void archives.refetch()}
                            disabled={archives.isRefetching}
                            aria-label={t('refresh')}
                        >
                            <RefreshCw className={'size-4' + (archives.isRefetching ? ' animate-spin' : '')} aria-hidden="true" />
                            {t('refresh')}
                        </Button>
                    </div>
                </div>

                {archives.isError ? (
                    <p className="rounded-2xl border border-border bg-muted/40 px-4 py-6 text-center text-sm text-muted-foreground">
                        {t('loadFailed')}
                    </p>
                ) : files.length === 0 ? (
                    <p className="rounded-2xl border border-border bg-muted/40 px-4 py-6 text-center text-sm text-muted-foreground">
                        {archives.isLoading ? t('loading') : t('empty')}
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
