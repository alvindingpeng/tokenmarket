import { useEffect, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { HeartPulse } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { useSettingList, useSetSetting, SettingKey } from '@/api/setting';
import { useMetricsToken, useRotateMetricsToken, useTestAlertWebhook } from '@/api/ops';
import { Copy } from 'lucide-react';
import { toast } from 'sonner';

// OPS_KEYS 是运营可靠性面板管理的设置键清单, 与后端 model/setting.go 对齐。
const OPS_KEYS = [
    SettingKey.LogRetentionDays,
    SettingKey.LogArchiveEnabled,
    SettingKey.LogStoreBody,
    SettingKey.LogMaskFields,
    SettingKey.AutoBackupEnabled,
    SettingKey.AutoBackupKeep,
    SettingKey.AutoBackupInterval,
    SettingKey.BackupEncrypt,
    SettingKey.BackupRemote,
    SettingKey.HealthCheckEnabled,
    SettingKey.HealthCheckInterval,
    SettingKey.HealthLatencyMS,
    SettingKey.AlertWebhookURL,
    SettingKey.AlertDedupMinutes,
    SettingKey.AlertFailStreak,
    SettingKey.MetricsAuth,
] as const;

// SettingOps 运营可靠性设置: 日志生命周期、自动备份、渠道健康与告警通知。
export function SettingOps() {
    const t = useTranslations('ops.settings');
    const tRoot = useTranslations('ops');
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();

    const testWebhook = useTestAlertWebhook();
    const [metricsAuth, setMetricsAuth] = useState(false);
    const [revealed, setRevealed] = useState('');
    // 仅在开启鉴权后才索取令牌, 关闭时不请求, 避免无谓地把凭据取到前端。
    const metricsToken = useMetricsToken(metricsAuth);
    const rotateToken = useRotateMetricsToken();

    const [values, setValues] = useState<Record<string, string>>({});
    const initial = useRef<Record<string, string>>({});

    useEffect(() => {
        if (!settings) return;
        const next: Record<string, string> = {};
        for (const key of OPS_KEYS) {
            const found = settings.find((item) => item.key === key);
            next[key] = found ? found.value : '';
        }
        setValues(next);
        initial.current = next;
        // 开关按已保存的模式初始化: 后端是唯一权威, 前端不另存一份状态。
        setMetricsAuth(next[SettingKey.MetricsAuth] === 'bearer');
    }, [settings]);

    // save 持久化单个设置键; 与初始值相同则跳过, 失败回滚输入。
    const save = (key: string, value: string) => {
        if ((initial.current[key] ?? '') === value) return;
        const before = initial.current[key] ?? '';
        setSetting.mutate({ key, value }, {
            onSuccess: () => {
                initial.current = { ...initial.current, [key]: value };
                toast.success(tRoot('saved'));
            },
            onError: (error) => {
                setValues((prev) => ({ ...prev, [key]: before }));
                toast.error(error instanceof Error ? error.message : tRoot('saveFailed'));
            },
        });
    };

    // saveNumber 数字输入: 失焦时夹取范围并保存。
    const saveNumber = (key: string, raw: string, min: number, max: number, fallback: string) => {
        const parsed = Number.parseInt(raw, 10);
        const normalized = Number.isFinite(parsed) ? String(Math.min(Math.max(parsed, min), max)) : fallback;
        setValues((prev) => ({ ...prev, [key]: normalized }));
        save(key, normalized);
    };

    const boolValue = (key: string) => (values[key] ?? '') === 'true';

    // 令牌读数与设置面板同步: 面板值优先, 未加载时回退到接口回显。
    const effectiveToken = values[SettingKey.MetricsAuth] === 'bearer' ? (revealed || metricsToken.data?.token || '') : '';

    const rowClass = 'flex flex-wrap items-center justify-between gap-3 py-3';
    const dividerClass = 'border-t border-border/60';

    return (
        <div className='space-y-1 rounded-3xl border border-border bg-card p-6'>
            <h2 className='flex items-center gap-2 pb-2 text-lg font-bold text-card-foreground'>
                <HeartPulse className='size-5' />
                {t('title')}
            </h2>

            <div className={rowClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('logRetention')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('logRetentionHint')}</p>
                </div>
                <Input type='number' min={1} max={3650} className='h-9 w-28 rounded-xl'
                    value={values[SettingKey.LogRetentionDays] ?? ''}
                    onBlur={(event) => saveNumber(SettingKey.LogRetentionDays, event.target.value.trim(), 1, 3650, '7')} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('logStoreBody')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('logStoreBodyHint')}</p>
                </div>
                <Switch checked={boolValue(SettingKey.LogStoreBody)}
                    onCheckedChange={(checked) => { setValues((prev) => ({ ...prev, [SettingKey.LogStoreBody]: String(checked) })); save(SettingKey.LogStoreBody, String(checked)); }} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('logArchive')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('logArchiveHint')}</p>
                </div>
                <Switch checked={boolValue(SettingKey.LogArchiveEnabled)}
                    onCheckedChange={(checked) => { setValues((prev) => ({ ...prev, [SettingKey.LogArchiveEnabled]: String(checked) })); save(SettingKey.LogArchiveEnabled, String(checked)); }} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0 flex-1'>
                    <Label className='text-sm font-medium'>{t('logMaskFields')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('logMaskFieldsHint')}</p>
                </div>
                <Input className='h-9 w-full max-w-72 rounded-xl' placeholder={t('logMaskFieldsHint')}
                    value={values[SettingKey.LogMaskFields] ?? ''}
                    onBlur={(event) => save(SettingKey.LogMaskFields, event.target.value.trim())} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('autoBackup')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('autoBackupHint')}</p>
                </div>
                <Switch checked={boolValue(SettingKey.AutoBackupEnabled)}
                    onCheckedChange={(checked) => { setValues((prev) => ({ ...prev, [SettingKey.AutoBackupEnabled]: String(checked) })); save(SettingKey.AutoBackupEnabled, String(checked)); }} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('autoBackupKeep')}</Label>
                </div>
                <Input type='number' min={1} max={365} className='h-9 w-28 rounded-xl'
                    value={values[SettingKey.AutoBackupKeep] ?? ''}
                    onBlur={(event) => saveNumber(SettingKey.AutoBackupKeep, event.target.value.trim(), 1, 365, '7')} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('autoBackupInterval')}</Label>
                </div>
                <Input type='number' min={1} max={720} className='h-9 w-28 rounded-xl'
                    value={values[SettingKey.AutoBackupInterval] ?? ''}
                    onBlur={(event) => saveNumber(SettingKey.AutoBackupInterval, event.target.value.trim(), 1, 720, '24')} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('backupEncrypt')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('backupEncryptHint')}</p>
                </div>
                <Switch checked={boolValue(SettingKey.BackupEncrypt)}
                    onCheckedChange={(checked) => { setValues((prev) => ({ ...prev, [SettingKey.BackupEncrypt]: String(checked) })); save(SettingKey.BackupEncrypt, String(checked)); }} />
            </div>

            <div className={rowClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('backupRemote')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('backupRemoteHint')}</p>
                </div>
                <Switch checked={boolValue(SettingKey.BackupRemote)}
                    onCheckedChange={(checked) => { setValues((prev) => ({ ...prev, [SettingKey.BackupRemote]: String(checked) })); save(SettingKey.BackupRemote, String(checked)); }} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('healthCheck')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('healthCheckHint')}</p>
                </div>
                <Switch checked={boolValue(SettingKey.HealthCheckEnabled)}
                    onCheckedChange={(checked) => { setValues((prev) => ({ ...prev, [SettingKey.HealthCheckEnabled]: String(checked) })); save(SettingKey.HealthCheckEnabled, String(checked)); }} />
            </div>

            <div className={rowClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('healthCheckInterval')}</Label>
                </div>
                <Input type='number' min={1} max={1440} className='h-9 w-28 rounded-xl'
                    value={values[SettingKey.HealthCheckInterval] ?? ''}
                    onBlur={(event) => saveNumber(SettingKey.HealthCheckInterval, event.target.value.trim(), 1, 1440, '30')} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('healthLatency')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('healthLatencyHint')}</p>
                </div>
                <Input type='number' min={0} max={600000} className='h-9 w-28 rounded-xl'
                    value={values[SettingKey.HealthLatencyMS] ?? ''}
                    onBlur={(event) => saveNumber(SettingKey.HealthLatencyMS, event.target.value.trim(), 0, 600000, '1000')} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0 flex-1'>
                    <Label className='text-sm font-medium'>{t('webhook')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('webhookHint')}</p>
                </div>
                <div className='flex w-full max-w-md items-center gap-2'>
                    <Input className='h-9 w-full rounded-xl' placeholder='https://...'
                        value={values[SettingKey.AlertWebhookURL] ?? ''}
                        onBlur={(event) => save(SettingKey.AlertWebhookURL, event.target.value.trim())} />
                    <Button type='button' variant='outline' size='sm' className='shrink-0 rounded-xl'
                        disabled={testWebhook.isPending || !(values[SettingKey.AlertWebhookURL] ?? '')}
                        onClick={() => testWebhook.mutate(undefined, {
                            onSuccess: () => toast.success(t('webhookTestOk')),
                            onError: (error) => toast.error(error instanceof Error ? error.message : t('webhookTestFail')),
                        })}>
                        {testWebhook.isPending ? t('webhookTesting') : t('webhookTest')}
                    </Button>
                </div>
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('alertDedup')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('alertDedupHint')}</p>
                </div>
                <Input type='number' min={0} max={1440} className='h-9 w-28 rounded-xl'
                    value={values[SettingKey.AlertDedupMinutes] ?? ''}
                    onBlur={(event) => saveNumber(SettingKey.AlertDedupMinutes, event.target.value.trim(), 0, 1440, '10')} />
            </div>

            <div className={rowClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('alertFailStreak')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('alertFailStreakHint')}</p>
                </div>
                <Input type='number' min={1} max={100} className='h-9 w-28 rounded-xl'
                    value={values[SettingKey.AlertFailStreak] ?? ''}
                    onBlur={(event) => saveNumber(SettingKey.AlertFailStreak, event.target.value.trim(), 1, 100, '5')} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('metricsAuth')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('metricsAuthHint')}</p>
                </div>
                <Switch checked={boolValue(SettingKey.MetricsAuth)}
                    onCheckedChange={(checked) => {
                        setMetricsAuth(checked);
                        setRevealed('');
                        setValues((prev) => ({ ...prev, [SettingKey.MetricsAuth]: checked ? 'bearer' : 'off' }));
                        save(SettingKey.MetricsAuth, checked ? 'bearer' : 'off');
                    }} />
            </div>

            {metricsAuth && (
                <div className={rowClass}>
                    <div className='min-w-0 flex-1'>
                        <Label className='text-sm font-medium'>{t('metricsToken')}</Label>
                        <p className='text-xs text-muted-foreground'>{t('metricsTokenHint')}</p>
                    </div>
                    <div className='flex w-full max-w-md items-center gap-2'>
                        <Input readOnly className='h-9 w-full rounded-xl font-mono text-xs' value={effectiveToken} />
                        <Button type='button' variant='outline' size='sm' className='shrink-0 rounded-xl'
                            disabled={!effectiveToken}
                            onClick={() => { navigator.clipboard.writeText(effectiveToken); toast.success(t('metricsTokenCopied')); }}>
                            <Copy className='size-4' />
                        </Button>
                        <Button type='button' variant='outline' size='sm' className='shrink-0 rounded-xl'
                            disabled={rotateToken.isPending}
                            onClick={() => rotateToken.mutate(undefined, {
                                onSuccess: (data) => { setRevealed(data.token); toast.success(t('metricsTokenRotated')); },
                                onError: (error) => toast.error(error instanceof Error ? error.message : tRoot('saveFailed')),
                            })}>
                            {t('metricsTokenRotate')}
                        </Button>
                    </div>
                </div>
            )}
        </div>
    );
}
