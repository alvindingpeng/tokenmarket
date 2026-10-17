import { useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';
import { HeartPulse } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { useSettingList, useSetSetting, SettingKey } from '@/api/setting';
import { useMetricsToken, useRotateMetricsToken, useTestAlertChannel, useTestAlertWebhook } from '@/api/ops';
import { Copy } from 'lucide-react';
import { toast } from 'sonner';
import { cn } from '@/lib/utils';

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
    SettingKey.BreakerKeyThreshold,
    SettingKey.BreakerKeyMinutes,
    SettingKey.BreakerKeyHardMinutes,
    SettingKey.BreakerModelThreshold,
    SettingKey.BreakerModelMinutes,
    SettingKey.BreakerCoolAuthSeconds,
    SettingKey.BreakerCoolSoftSeconds,
    SettingKey.AlertWebhookURL,
    SettingKey.AlertDedupMinutes,
    SettingKey.AlertFailStreak,
    SettingKey.AlertChannels,
    SettingKey.AlertTelegramBotToken,
    SettingKey.AlertTelegramChatID,
    SettingKey.AlertEmailSMTPHost,
    SettingKey.AlertEmailSMTPPort,
    SettingKey.AlertEmailUsername,
    SettingKey.AlertEmailPassword,
    SettingKey.AlertEmailFrom,
    SettingKey.AlertEmailTo,
    SettingKey.MetricsAuth,
] as const;

// CHANNEL_LABELS 告警渠道展示名与 i18n key 的对应。
const CHANNEL_LABELS = {
    webhook: 'alertChannelWebhook',
    telegram: 'alertChannelTelegram',
    email: 'alertChannelEmail',
} as const;

// SettingOps 运营可靠性设置: 日志生命周期、自动备份、渠道健康与告警通知。
export function SettingOps() {
    const t = useTranslations('ops.settings');
    const tRoot = useTranslations('ops');
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();

    const testWebhook = useTestAlertWebhook();
    const testChannel = useTestAlertChannel();

    // runChannelTest 发送测试告警并就地反馈结果。
    const runChannelTest = (channel: 'telegram' | 'email') => {
        testChannel.mutate(channel, {
            onSuccess: () => toast.success(t('alertTestOk')),
            onError: (error) => toast.error(error instanceof Error ? error.message : t('alertTestFail')),
        });
    };
    const [revealed, setRevealed] = useState('');

    // 服务端设置是唯一权威: 已保存值直接派生, 本地只存草稿。
    // 早期版本用 effect 把服务端值搬进 state, 每次加载都会多一轮渲染, 还可能覆盖用户正在输入的内容。
    const savedValues = useMemo(() => {
        const next: Record<string, string> = {};
        for (const key of OPS_KEYS) {
            const found = settings?.find((item) => item.key === key);
            next[key] = found ? found.value : '';
        }
        return next;
    }, [settings]);
    const [draft, setDraft] = useState<Record<string, string>>({});
    const values = useMemo(() => ({ ...savedValues, ...draft }), [savedValues, draft]);
    // 鉴权开关同样派生自设置值, 不再单独存一份状态。
    const metricsAuth = values[SettingKey.MetricsAuth] === 'bearer';

    // 仅在开启鉴权后才索取令牌, 关闭时不请求, 避免无谓地把凭据取到前端。
    const metricsToken = useMetricsToken(metricsAuth);
    const rotateToken = useRotateMetricsToken();

    // save 持久化单个设置键; 与已保存值相同则跳过, 失败把草稿回退到服务端值。
    const save = (key: string, value: string) => {
        if (savedValues[key] === value) return;
        setSetting.mutate({ key, value }, {
            onSuccess: () => {
                toast.success(tRoot('saved'));
            },
            onError: (error) => {
                setDraft((prev) => ({ ...prev, [key]: savedValues[key] ?? '' }));
                toast.error(error instanceof Error ? error.message : tRoot('saveFailed'));
            },
        });
    };

    // saveNumber 数字输入: 失焦时夹取范围并保存。
    // 数字框必须挂 onChange 只写本地草稿、失焦才提交: 受控输入只有 value 而没有 onChange 时,
    // React 会把每次按键都回滚掉, 输入框看着能聚焦却永远打不进字。
    const saveNumber = (key: string, raw: string, min: number, max: number, fallback: string) => {
        const parsed = Number.parseInt(raw, 10);
        const normalized = Number.isFinite(parsed) ? String(Math.min(Math.max(parsed, min), max)) : fallback;
        setDraft((prev) => ({ ...prev, [key]: normalized }));
        save(key, normalized);
    };

    const boolValue = (key: string) => (values[key] ?? '') === 'true';

    // alertChannels 解析启用的告警渠道; 空值与后端一致按仅 webhook 展示。
    const alertChannels = (() => {
        const raw = (values[SettingKey.AlertChannels] ?? '').trim();
        return raw === '' ? ['webhook'] : raw.split(',').map((item) => item.trim()).filter(Boolean);
    })();

    // toggleAlertChannel 勾选/取消告警渠道并立即保存; 至少保留一个(全关时后端会回落到 webhook, 展示会不一致)。
    const toggleAlertChannel = (name: string, on: boolean) => {
        const next = on ? [...new Set([...alertChannels, name])] : alertChannels.filter((item) => item !== name);
        if (next.length === 0) return;
        const value = next.join(',');
        setDraft((prev) => ({ ...prev, [SettingKey.AlertChannels]: value }));
        save(SettingKey.AlertChannels, value);
    };

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
                    onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.LogRetentionDays]: event.target.value }))}
                    onBlur={(event) => saveNumber(SettingKey.LogRetentionDays, event.target.value.trim(), 1, 3650, '7')} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('logStoreBody')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('logStoreBodyHint')}</p>
                </div>
                <Switch checked={boolValue(SettingKey.LogStoreBody)}
                    onCheckedChange={(checked) => { setDraft((prev) => ({ ...prev, [SettingKey.LogStoreBody]: String(checked) })); save(SettingKey.LogStoreBody, String(checked)); }} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('logArchive')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('logArchiveHint')}</p>
                </div>
                <Switch checked={boolValue(SettingKey.LogArchiveEnabled)}
                    onCheckedChange={(checked) => { setDraft((prev) => ({ ...prev, [SettingKey.LogArchiveEnabled]: String(checked) })); save(SettingKey.LogArchiveEnabled, String(checked)); }} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0 flex-1'>
                    <Label className='text-sm font-medium'>{t('logMaskFields')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('logMaskFieldsHint')}</p>
                </div>
                <Input className='h-9 w-full max-w-72 rounded-xl' placeholder={t('logMaskFieldsHint')}
                    value={values[SettingKey.LogMaskFields] ?? ''}
                    onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.LogMaskFields]: event.target.value }))}
                    onBlur={(event) => save(SettingKey.LogMaskFields, event.target.value.trim())} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('autoBackup')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('autoBackupHint')}</p>
                </div>
                <Switch checked={boolValue(SettingKey.AutoBackupEnabled)}
                    onCheckedChange={(checked) => { setDraft((prev) => ({ ...prev, [SettingKey.AutoBackupEnabled]: String(checked) })); save(SettingKey.AutoBackupEnabled, String(checked)); }} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('autoBackupKeep')}</Label>
                </div>
                <Input type='number' min={1} max={365} className='h-9 w-28 rounded-xl'
                    value={values[SettingKey.AutoBackupKeep] ?? ''}
                    onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.AutoBackupKeep]: event.target.value }))}
                    onBlur={(event) => saveNumber(SettingKey.AutoBackupKeep, event.target.value.trim(), 1, 365, '7')} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('autoBackupInterval')}</Label>
                </div>
                <Input type='number' min={1} max={720} className='h-9 w-28 rounded-xl'
                    value={values[SettingKey.AutoBackupInterval] ?? ''}
                    onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.AutoBackupInterval]: event.target.value }))}
                    onBlur={(event) => saveNumber(SettingKey.AutoBackupInterval, event.target.value.trim(), 1, 720, '24')} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('backupEncrypt')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('backupEncryptHint')}</p>
                </div>
                <Switch checked={boolValue(SettingKey.BackupEncrypt)}
                    onCheckedChange={(checked) => { setDraft((prev) => ({ ...prev, [SettingKey.BackupEncrypt]: String(checked) })); save(SettingKey.BackupEncrypt, String(checked)); }} />
            </div>

            <div className={rowClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('backupRemote')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('backupRemoteHint')}</p>
                </div>
                <Switch checked={boolValue(SettingKey.BackupRemote)}
                    onCheckedChange={(checked) => { setDraft((prev) => ({ ...prev, [SettingKey.BackupRemote]: String(checked) })); save(SettingKey.BackupRemote, String(checked)); }} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('healthCheck')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('healthCheckHint')}</p>
                </div>
                <Switch checked={boolValue(SettingKey.HealthCheckEnabled)}
                    onCheckedChange={(checked) => { setDraft((prev) => ({ ...prev, [SettingKey.HealthCheckEnabled]: String(checked) })); save(SettingKey.HealthCheckEnabled, String(checked)); }} />
            </div>

            <div className={rowClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('healthCheckInterval')}</Label>
                </div>
                <Input type='number' min={1} max={1440} className='h-9 w-28 rounded-xl'
                    value={values[SettingKey.HealthCheckInterval] ?? ''}
                    onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.HealthCheckInterval]: event.target.value }))}
                    onBlur={(event) => saveNumber(SettingKey.HealthCheckInterval, event.target.value.trim(), 1, 1440, '30')} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('healthLatency')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('healthLatencyHint')}</p>
                </div>
                <Input type='number' min={0} max={600000} className='h-9 w-28 rounded-xl'
                    value={values[SettingKey.HealthLatencyMS] ?? ''}
                    onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.HealthLatencyMS]: event.target.value }))}
                    onBlur={(event) => saveNumber(SettingKey.HealthLatencyMS, event.target.value.trim(), 0, 600000, '1000')} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('breakerKeyThreshold')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('breakerKeyThresholdHint')}</p>
                </div>
                <Input type='number' min={0} max={100} className='h-9 w-28 rounded-xl'
                    value={values[SettingKey.BreakerKeyThreshold] ?? ''}
                    onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.BreakerKeyThreshold]: event.target.value }))}
                    onBlur={(event) => saveNumber(SettingKey.BreakerKeyThreshold, event.target.value.trim(), 0, 100, '5')} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('breakerKeyMinutes')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('breakerKeyMinutesHint')}</p>
                </div>
                <Input type='number' min={1} max={1440} className='h-9 w-28 rounded-xl'
                    value={values[SettingKey.BreakerKeyMinutes] ?? ''}
                    onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.BreakerKeyMinutes]: event.target.value }))}
                    onBlur={(event) => saveNumber(SettingKey.BreakerKeyMinutes, event.target.value.trim(), 1, 1440, '5')} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('breakerKeyHardMinutes')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('breakerKeyHardMinutesHint')}</p>
                </div>
                <Input type='number' min={1} max={1440} className='h-9 w-28 rounded-xl'
                    value={values[SettingKey.BreakerKeyHardMinutes] ?? ''}
                    onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.BreakerKeyHardMinutes]: event.target.value }))}
                    onBlur={(event) => saveNumber(SettingKey.BreakerKeyHardMinutes, event.target.value.trim(), 1, 1440, '30')} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('breakerModelThreshold')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('breakerModelThresholdHint')}</p>
                </div>
                <Input type='number' min={0} max={100} className='h-9 w-28 rounded-xl'
                    value={values[SettingKey.BreakerModelThreshold] ?? ''}
                    onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.BreakerModelThreshold]: event.target.value }))}
                    onBlur={(event) => saveNumber(SettingKey.BreakerModelThreshold, event.target.value.trim(), 0, 100, '5')} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('breakerModelMinutes')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('breakerModelMinutesHint')}</p>
                </div>
                <Input type='number' min={1} max={1440} className='h-9 w-28 rounded-xl'
                    value={values[SettingKey.BreakerModelMinutes] ?? ''}
                    onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.BreakerModelMinutes]: event.target.value }))}
                    onBlur={(event) => saveNumber(SettingKey.BreakerModelMinutes, event.target.value.trim(), 1, 1440, '5')} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('breakerCoolAuthSeconds')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('breakerCoolAuthSecondsHint')}</p>
                </div>
                <Input type='number' min={0} max={600} className='h-9 w-28 rounded-xl'
                    value={values[SettingKey.BreakerCoolAuthSeconds] ?? ''}
                    onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.BreakerCoolAuthSeconds]: event.target.value }))}
                    onBlur={(event) => saveNumber(SettingKey.BreakerCoolAuthSeconds, event.target.value.trim(), 0, 600, '60')} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('breakerCoolSoftSeconds')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('breakerCoolSoftSecondsHint')}</p>
                </div>
                <Input type='number' min={0} max={600} className='h-9 w-28 rounded-xl'
                    value={values[SettingKey.BreakerCoolSoftSeconds] ?? ''}
                    onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.BreakerCoolSoftSeconds]: event.target.value }))}
                    onBlur={(event) => saveNumber(SettingKey.BreakerCoolSoftSeconds, event.target.value.trim(), 0, 600, '30')} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0 flex-1'>
                    <Label className='text-sm font-medium'>{t('webhook')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('webhookHint')}</p>
                </div>
                <div className='flex w-full max-w-md items-center gap-2'>
                    <Input className='h-9 w-full rounded-xl' placeholder='https://...'
                        value={values[SettingKey.AlertWebhookURL] ?? ''}
                        onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.AlertWebhookURL]: event.target.value }))}
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
                    <Label className='text-sm font-medium'>{t('alertChannels')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('alertChannelsHint')}</p>
                </div>
                <div className='flex items-center gap-2'>
                    {(['webhook', 'telegram', 'email'] as const).map((name) => {
                        const on = alertChannels.includes(name);
                        return (
                            <button key={name} type='button'
                                className={cn('rounded-xl border px-3 py-1.5 text-xs transition-colors', on ? 'border-primary bg-primary/10 font-medium text-primary' : 'border-border text-muted-foreground hover:bg-muted')}
                                onClick={() => toggleAlertChannel(name, !on)}>
                                {t(CHANNEL_LABELS[name])}
                            </button>
                        );
                    })}
                </div>
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('telegramBotToken')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('telegramBotTokenHint')}</p>
                </div>
                <Input className='h-9 w-full max-w-md rounded-xl' placeholder='123456789:ABCdef...' autoComplete='off'
                    value={values[SettingKey.AlertTelegramBotToken] ?? ''}
                    onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.AlertTelegramBotToken]: event.target.value }))}
                    onBlur={(event) => save(SettingKey.AlertTelegramBotToken, event.target.value.trim())} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('telegramChatID')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('telegramChatIDHint')}</p>
                </div>
                <div className='flex w-full max-w-md items-center gap-2'>
                    <Input className='h-9 w-full rounded-xl' placeholder='-1001234567890' autoComplete='off'
                        value={values[SettingKey.AlertTelegramChatID] ?? ''}
                        onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.AlertTelegramChatID]: event.target.value }))}
                        onBlur={(event) => save(SettingKey.AlertTelegramChatID, event.target.value.trim())} />
                    <Button type='button' variant='outline' size='sm' className='shrink-0 rounded-xl'
                        disabled={testChannel.isPending}
                        onClick={() => runChannelTest('telegram')}>
                        {testChannel.isPending ? t('alertTesting') : t('alertTest')}
                    </Button>
                </div>
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('emailSMTP')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('emailSMTPHint')}</p>
                </div>
                <div className='flex w-full max-w-md items-center gap-2'>
                    <Input className='h-9 w-full rounded-xl' placeholder='smtp.example.com'
                        value={values[SettingKey.AlertEmailSMTPHost] ?? ''}
                        onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.AlertEmailSMTPHost]: event.target.value }))}
                        onBlur={(event) => save(SettingKey.AlertEmailSMTPHost, event.target.value.trim())} />
                    <Input type='number' min={1} max={65535} className='h-9 w-24 shrink-0 rounded-xl' placeholder='465'
                        value={values[SettingKey.AlertEmailSMTPPort] ?? ''}
                        onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.AlertEmailSMTPPort]: event.target.value }))}
                        onBlur={(event) => saveNumber(SettingKey.AlertEmailSMTPPort, event.target.value.trim(), 1, 65535, '465')} />
                </div>
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('emailAccount')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('emailAccountHint')}</p>
                </div>
                <div className='flex w-full max-w-md items-center gap-2'>
                    <Input className='h-9 w-full rounded-xl' placeholder={t('emailUsernamePlaceholder')} autoComplete='off'
                        value={values[SettingKey.AlertEmailUsername] ?? ''}
                        onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.AlertEmailUsername]: event.target.value }))}
                        onBlur={(event) => save(SettingKey.AlertEmailUsername, event.target.value.trim())} />
                    <Input type='password' className='h-9 w-full rounded-xl' placeholder={t('emailPasswordPlaceholder')} autoComplete='off'
                        value={values[SettingKey.AlertEmailPassword] ?? ''}
                        onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.AlertEmailPassword]: event.target.value }))}
                        onBlur={(event) => save(SettingKey.AlertEmailPassword, event.target.value)} />
                </div>
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('emailFromTo')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('emailFromToHint')}</p>
                </div>
                <div className='flex w-full max-w-md items-center gap-2'>
                    <Input className='h-9 w-full rounded-xl' placeholder='alerts@example.com'
                        value={values[SettingKey.AlertEmailFrom] ?? ''}
                        onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.AlertEmailFrom]: event.target.value }))}
                        onBlur={(event) => save(SettingKey.AlertEmailFrom, event.target.value.trim())} />
                    <Input className='h-9 w-full rounded-xl' placeholder={t('emailToPlaceholder')}
                        value={values[SettingKey.AlertEmailTo] ?? ''}
                        onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.AlertEmailTo]: event.target.value }))}
                        onBlur={(event) => save(SettingKey.AlertEmailTo, event.target.value.trim())} />
                    <Button type='button' variant='outline' size='sm' className='shrink-0 rounded-xl'
                        disabled={testChannel.isPending}
                        onClick={() => runChannelTest('email')}>
                        {testChannel.isPending ? t('alertTesting') : t('alertTest')}
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
                    onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.AlertDedupMinutes]: event.target.value }))}
                    onBlur={(event) => saveNumber(SettingKey.AlertDedupMinutes, event.target.value.trim(), 0, 1440, '10')} />
            </div>

            <div className={rowClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('alertFailStreak')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('alertFailStreakHint')}</p>
                </div>
                <Input type='number' min={1} max={100} className='h-9 w-28 rounded-xl'
                    value={values[SettingKey.AlertFailStreak] ?? ''}
                    onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.AlertFailStreak]: event.target.value }))}
                    onBlur={(event) => saveNumber(SettingKey.AlertFailStreak, event.target.value.trim(), 1, 100, '5')} />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className='min-w-0'>
                    <Label className='text-sm font-medium'>{t('metricsAuth')}</Label>
                    <p className='text-xs text-muted-foreground'>{t('metricsAuthHint')}</p>
                </div>
                <Switch checked={boolValue(SettingKey.MetricsAuth)}
                    onCheckedChange={(checked) => {
                        setRevealed('');
                        setDraft((prev) => ({ ...prev, [SettingKey.MetricsAuth]: checked ? 'bearer' : 'off' }));
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
