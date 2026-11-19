import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import {
    checkWebhookSecret,
    checkWebhookUrl,
    redactWebhookError,
    useCreateWebhook,
    useUpdateWebhook,
    WEBHOOK_EVENTS,
    WEBHOOK_RETRY_MAX,
    WEBHOOK_RETRY_MIN,
    WEBHOOK_SECRET_MAX_BYTES,
    WEBHOOK_SECRET_MIN_BYTES,
    WEBHOOK_URL_MAX_LEN,
    type WebhookSecretCheck,
    type WebhookUrlCheck,
    type WebhookView,
} from '@/api/webhook';
import { notificationTypeKey } from '@/api/notification';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';

// WebhookForm 创建/编辑订阅表单(对话框内容体, 也可脱离对话框独立渲染做 SSR 断言)。
// 安全红线:
//   - 密钥输入框永为空串、type=password、提交后即丢弃: 不回显、不缓存、不进 toast;
//   - URL/密钥只进请求体, 报错文案经 redactWebhookError 抹除后再进 toast。
// 校验口径与服务端同向: 地址 https 且 443, 密钥 16..128 字节(创建必填/编辑留空保留),
// 事件至少一个, retry_count 0..3; 服务端校验才是权威, 这里只为即时反馈。
export function WebhookForm({ mode, initial, onSaved, onCancel }: {
    mode: 'create' | 'edit';
    initial?: WebhookView;
    onSaved: () => void;
    onCancel: () => void;
}) {
    const t = useTranslations('webhook');
    const tEvent = useTranslations('notification');
    const create = useCreateWebhook();
    const update = useUpdateWebhook();
    const mutation = mode === 'create' ? create : update;

    const [url, setUrl] = useState(initial?.url ?? '');
    // 密钥永远从空串起步: 编辑时后端只回 has_secret, 界面没有任何可回显的值。
    const [secret, setSecret] = useState('');
    const [events, setEvents] = useState<string[]>(initial?.events ?? []);
    const [enabled, setEnabled] = useState(initial?.enabled ?? true);
    const [retryCount, setRetryCount] = useState(String(initial?.retry_count ?? 3));
    const [errors, setErrors] = useState<{ url?: WebhookUrlCheck; secret?: WebhookSecretCheck; events?: boolean }>({});

    const toggleEvent = (event: string, checked: boolean) => {
        setEvents((previous) => checked ? [...new Set([...previous, event])] : previous.filter((item) => item !== event));
        setErrors((previous) => ({ ...previous, events: undefined }));
    };

    const handleSubmit = (submitEvent: React.FormEvent<HTMLFormElement>) => {
        submitEvent.preventDefault();
        if (mutation.isPending) return;

        const urlCheck = checkWebhookUrl(url);
        const secretCheck = checkWebhookSecret(secret, mode);
        const eventsOk = events.length > 0;
        setErrors({
            url: urlCheck !== 'ok' ? urlCheck : undefined,
            secret: secretCheck !== 'ok' ? secretCheck : undefined,
            events: !eventsOk,
        });
        if (urlCheck !== 'ok' || secretCheck !== 'ok' || !eventsOk) return;

        const payload = {
            url: url.trim(),
            events,
            enabled,
            retry_count: Number(retryCount),
            // 编辑留空的密钥交给 updateWebhook 按「不上行=保留」处理。
            secret,
        };
        // URL 可能带 token、密钥是凭据: 进 toast 的错误文案一律先抹除这两样。
        const sensitive = [payload.url, secret];
        const callbacks = {
            onSuccess: () => {
                toast.success(t(mode === 'create' ? 'form.toast.created' : 'form.toast.updated'));
                onSaved();
            },
            onError: (error: Error) => toast.error(t(mode === 'create' ? 'form.toast.createFailed' : 'form.toast.updateFailed'), {
                description: redactWebhookError(error.message, sensitive),
            }),
        };
        if (mode === 'create') {
            create.mutate(payload, callbacks);
        } else {
            update.mutate({ id: initial?.id ?? 0, ...payload }, callbacks);
        }
    };

    const urlError = errors.url && errors.url !== 'ok'
        ? t(errors.url === 'empty' ? 'form.urlRequired' : errors.url === 'invalid' ? 'form.urlInvalid' : errors.url === 'httpsOnly' ? 'form.urlHttps' : 'form.urlPort')
        : null;
    const secretError = errors.secret && errors.secret !== 'ok'
        ? t(errors.secret === 'required' ? 'form.secretRequired' : errors.secret === 'tooShort' ? 'form.secretErrorShort' : 'form.secretErrorLong')
        : null;

    return (
        <div className="w-screen max-w-full md:max-w-lg">
            <h3 className="mb-4 text-lg font-bold text-card-foreground">
                {t(mode === 'create' ? 'form.createTitle' : 'form.editTitle')}
            </h3>
            <form onSubmit={handleSubmit} className="space-y-4">
                <div className="space-y-2">
                    <Label htmlFor="webhook-url">{t('form.urlLabel')}</Label>
                    <Input
                        id="webhook-url"
                        type="url"
                        inputMode="url"
                        value={url}
                        onChange={(event) => {
                            setUrl(event.target.value);
                            setErrors((previous) => ({ ...previous, url: undefined }));
                        }}
                        className="rounded-xl font-mono text-sm"
                        placeholder={t('form.urlPlaceholder')}
                        maxLength={WEBHOOK_URL_MAX_LEN}
                        autoComplete="off"
                        spellCheck={false}
                        aria-invalid={urlError ? true : undefined}
                    />
                    {urlError && <p className="text-xs text-destructive">{urlError}</p>}
                    <p className="text-xs text-muted-foreground">{t('hints.httpsOnly')}</p>
                </div>

                <div className="space-y-2">
                    <Label htmlFor="webhook-secret">{t('form.secretLabel')}</Label>
                    <Input
                        id="webhook-secret"
                        type="password"
                        value={secret}
                        onChange={(event) => {
                            setSecret(event.target.value);
                            setErrors((previous) => ({ ...previous, secret: undefined }));
                        }}
                        className="rounded-xl"
                        autoComplete="new-password"
                        aria-invalid={secretError ? true : undefined}
                    />
                    {secretError && <p className="text-xs text-destructive">{secretError}</p>}
                    <p className="text-xs text-muted-foreground">
                        {mode === 'create'
                            ? t('form.secretCreateHint', { min: WEBHOOK_SECRET_MIN_BYTES, max: WEBHOOK_SECRET_MAX_BYTES })
                            : initial?.has_secret
                                ? t('form.secretEditHint') + ' \u00b7 ' + t('form.secretSet')
                                : t('form.secretEditHint')}
                    </p>
                    <p className="text-xs text-muted-foreground">{t('hints.secretWriteOnly')}</p>
                </div>

                <div className="space-y-2">
                    <Label>{t('form.eventsLabel')}</Label>
                    <div className="grid grid-cols-1 gap-2 rounded-2xl border border-border p-3 sm:grid-cols-2">
                        {WEBHOOK_EVENTS.map((event) => (
                            <label key={event} className="flex cursor-pointer items-center gap-2 text-sm">
                                <Checkbox
                                    checked={events.includes(event)}
                                    onCheckedChange={(checked) => toggleEvent(event, checked === true)}
                                />
                                <span>{tEvent(notificationTypeKey(event))}</span>
                            </label>
                        ))}
                    </div>
                    {errors.events && <p className="text-xs text-destructive">{t('form.eventsError')}</p>}
                    <p className="text-xs text-muted-foreground">{t('form.eventsHint')}</p>
                </div>

                <div className="flex items-center justify-between gap-4 rounded-2xl border border-border px-3 py-2">
                    <Label htmlFor="webhook-enabled" className="cursor-pointer">{t('form.enabledLabel')}</Label>
                    <Switch id="webhook-enabled" checked={enabled} onCheckedChange={setEnabled} />
                </div>

                <div className="space-y-2">
                    <Label htmlFor="webhook-retry">{t('form.retryLabel')}</Label>
                    <Select value={retryCount} onValueChange={setRetryCount}>
                        <SelectTrigger id="webhook-retry" className="w-32 rounded-xl">
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            {Array.from({ length: WEBHOOK_RETRY_MAX - WEBHOOK_RETRY_MIN + 1 }, (_, index) => String(WEBHOOK_RETRY_MIN + index)).map((option) => (
                                <SelectItem key={option} value={option}>{option}</SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                    <p className="text-xs text-muted-foreground">{t('form.retryHint')}</p>
                </div>

                {/* 投递约定: 5 秒超时、至少一次(按 delivery id 去重)、仅通用 JSON 签名。 */}
                <ul className="space-y-1 rounded-2xl bg-muted/50 px-3 py-2 text-xs text-muted-foreground">
                    <li>{t('hints.timeout')}</li>
                    <li>{t('hints.atLeastOnce')}</li>
                    <li>{t('hints.jsonOnly')}</li>
                </ul>

                <div className="flex justify-end gap-2 pt-2">
                    <Button type="button" variant="secondary" onClick={onCancel} className="h-9 rounded-xl px-4">
                        {t('form.cancel')}
                    </Button>
                    <Button type="submit" disabled={mutation.isPending} className="h-9 rounded-xl px-4">
                        {mutation.isPending
                            ? t('form.saving')
                            : t(mode === 'create' ? 'form.submitCreate' : 'form.submitEdit')}
                    </Button>
                </div>
            </form>
        </div>
    );
}
