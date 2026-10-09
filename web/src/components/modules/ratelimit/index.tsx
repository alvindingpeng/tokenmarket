import { useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';
import { AlertCircle, Gauge, Loader2, Pencil, Plus, RefreshCw, Trash2, X } from 'lucide-react';
import {
    RateLimitPolicy,
    RatePolicyInput,
    RateScopeType,
    useDeleteRatePolicy,
    useRateInspect,
    useRatePolicies,
    useRateUsage,
    useSaveRatePolicy,
} from '@/api/ratelimit';
import { useAPIKeyList } from '@/api/apikey';
import { useGroupList } from '@/api/group';
import { useChannelStats } from '@/api/channel';
import { useUserList } from '@/api/usermanage';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';

const SCOPE_TYPES: RateScopeType[] = ['system', 'user', 'api_key', 'group', 'channel', 'channel_key', 'channel_model'];

function formatTime(value: string): string {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

function formatTokens(value: number): string {
    return value.toLocaleString();
}

export default function RateLimit() {
    const t = useTranslations('ratelimit');
    const [filterScope, setFilterScope] = useState('');
    const [editing, setEditing] = useState<RatePolicyInput | null>(null);
    const [inspectScope, setInspectScope] = useState<{ type: RateScopeType; id: number; model: string }>({ type: 'system', id: 0, model: '' });

    const { data: policyData, isLoading, isError, refetch, isRefetching } = useRatePolicies();
    const { data: userData } = useUserList();
    const { data: keyData } = useAPIKeyList();
    const { data: groupData } = useGroupList();
    const { data: channelData } = useChannelStats();
    const savePolicy = useSaveRatePolicy();
    const deletePolicy = useDeleteRatePolicy();
    const inspect = useRateInspect(inspectScope.type, inspectScope.id, inspectScope.model);
    const usage = useRateUsage(24, inspectScope.type, inspectScope.id);

    // 范围目标名称映射: 策略与用量展示都按此把主键翻译成人可读名称。
    const scopeNames = useMemo(() => {
        const map: Record<string, string> = {};
        (userData ?? []).forEach((item) => { map['user:' + item.id] = item.username; });
        (keyData ?? []).forEach((item) => { map['api_key:' + item.id] = item.name; });
        (groupData ?? []).forEach((item: { id: number; name: string }) => { map['group:' + item.id] = item.name; });
        (channelData ?? []).forEach((item) => { map['channel:' + item.channel_id] = item.channel_name; });
        return map;
    }, [userData, keyData, groupData, channelData]);

    // 范围目标候选: 有列表的范围用下拉, 其余(凭据/渠道模型)直接填主键。
    const scopeOptions = useMemo(() => {
        switch (editing?.scope_type) {
            case 'user':
                return (userData ?? []).map((item) => ({ value: item.id, label: item.username }));
            case 'api_key':
                return (keyData ?? []).map((item) => ({ value: item.id, label: item.name }));
            case 'group':
                return (groupData ?? []).map((item: { id: number; name: string }) => ({ value: item.id, label: item.name }));
            case 'channel':
                return (channelData ?? []).map((item) => ({ value: item.channel_id, label: item.channel_name }));
            default:
                return [];
        }
    }, [editing?.scope_type, userData, keyData, groupData, channelData]);

    const policies = (policyData?.items ?? []).filter((item) => !filterScope || item.scope_type === filterScope);

    const scopeLabel = (type: string, id: number) => {
        if (type === 'system') return t('scope.system');
        const name = scopeNames[type + ':' + id];
        return name ? name + ' (#' + id + ')' : '#' + id;
    };

    const startCreate = () =>
        setEditing({ scope_type: 'user', scope_id: 0, model_name: '', rpm: 60, tpm: 0, concurrent: 0, enabled: true });
    const startEdit = (policy: RateLimitPolicy) =>
        setEditing({
            id: policy.id,
            scope_type: policy.scope_type,
            scope_id: policy.scope_id,
            model_name: policy.model_name,
            rpm: policy.rpm,
            tpm: policy.tpm,
            concurrent: policy.concurrent ?? 0,
            enabled: policy.enabled,
        });
    const save = () => {
        if (!editing) return;
        savePolicy.mutate(editing, { onSuccess: () => setEditing(null) });
    };

    return (
        <div className="flex h-full min-h-0 flex-col gap-3">
            <div className="flex shrink-0 items-center justify-between gap-4">
                <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                    <Gauge className="size-5" />
                    {t('title')}
                    <span className="text-sm font-normal text-muted-foreground">{t('total', { total: policies.length })}</span>
                </h2>
                <div className="flex gap-2">
                    <Button variant="outline" size="sm" onClick={() => void refetch()} disabled={isRefetching} className="rounded-xl">
                        <RefreshCw className={'size-4' + (isRefetching ? ' animate-spin' : '')} />
                        {t('refresh')}
                    </Button>
                    <Button size="sm" onClick={startCreate} className="rounded-xl">
                        <Plus className="size-4" />
                        {t('create')}
                    </Button>
                </div>
            </div>

            <div className="flex shrink-0 flex-wrap items-center gap-2">
                <Select value={filterScope} onValueChange={setFilterScope}>
                    <SelectTrigger className="w-48 rounded-xl"><SelectValue placeholder={t('filters.scopeAll')} /></SelectTrigger>
                    <SelectContent>
                        <SelectItem value="">{t('filters.scopeAll')}</SelectItem>
                        {SCOPE_TYPES.map((value) => <SelectItem key={value} value={value}>{t('scope.' + value)}</SelectItem>)}
                    </SelectContent>
                </Select>
            </div>

            {editing && (
                <div className="shrink-0 rounded-3xl border border-border bg-card p-4">
                    <div className="mb-3 flex items-center justify-between">
                        <h3 className="font-semibold text-card-foreground">{editing.id ? t('edit') : t('create')}</h3>
                        <button type="button" aria-label={t('filters.clear')} onClick={() => setEditing(null)} className="text-muted-foreground hover:text-foreground"><X className="size-4" /></button>
                    </div>
                    <div className="flex flex-wrap items-end gap-3">
                        <div className="flex flex-col gap-1">
                            <span className="text-xs text-muted-foreground">{t('form.scopeType')}</span>
                            <Select value={editing.scope_type} onValueChange={(value) => setEditing({ ...editing, scope_type: value as RateScopeType, scope_id: 0 })}>
                                <SelectTrigger className="w-44 rounded-xl"><SelectValue /></SelectTrigger>
                                <SelectContent>
                                    {SCOPE_TYPES.map((value) => <SelectItem key={value} value={value}>{t('scope.' + value)}</SelectItem>)}
                                </SelectContent>
                            </Select>
                        </div>
                        {editing.scope_type !== 'system' && (
                            <div className="flex flex-col gap-1">
                                <span className="text-xs text-muted-foreground">{t('form.scopeTarget')}</span>
                                {scopeOptions.length > 0 ? (
                                    <Select
                                        value={editing.scope_id ? String(editing.scope_id) : ''}
                                        onValueChange={(value) => setEditing({ ...editing, scope_id: Number(value) })}
                                    >
                                        <SelectTrigger className="w-48 rounded-xl"><SelectValue placeholder={t('form.scopeTargetHint')} /></SelectTrigger>
                                        <SelectContent>
                                            {scopeOptions.map((option) => <SelectItem key={option.value} value={String(option.value)}>{option.label}</SelectItem>)}
                                        </SelectContent>
                                    </Select>
                                ) : (
                                    <Input
                                        className="h-9 w-36 rounded-xl"
                                        placeholder={t('form.scopeIdHint')}
                                        value={editing.scope_id ? String(editing.scope_id) : ''}
                                        onChange={(event) => setEditing({ ...editing, scope_id: Number(event.target.value) || 0 })}
                                    />
                                )}
                            </div>
                        )}
                        <div className="flex flex-col gap-1">
                            <span className="text-xs text-muted-foreground">{t('form.modelName')}</span>
                            <Input
                                className="h-9 w-44 rounded-xl"
                                placeholder={t('form.modelNameHint')}
                                value={editing.model_name}
                                onChange={(event) => setEditing({ ...editing, model_name: event.target.value })}
                            />
                        </div>
                        <div className="flex flex-col gap-1">
                            <span className="text-xs text-muted-foreground">{t('form.rpm')}</span>
                            <Input
                                type="number"
                                className="h-9 w-28 rounded-xl"
                                value={editing.rpm}
                                onChange={(event) => setEditing({ ...editing, rpm: Number(event.target.value) || 0 })}
                            />
                        </div>
                        <div className="flex flex-col gap-1">
                            <span className="text-xs text-muted-foreground">{t('form.tpm')}</span>
                            <Input
                                type="number"
                                className="h-9 w-32 rounded-xl"
                                value={editing.tpm}
                                onChange={(event) => setEditing({ ...editing, tpm: Number(event.target.value) || 0 })}
                            />
                        </div>
                        <div className="flex flex-col gap-1">
                            <span className="text-xs text-muted-foreground">{t('concurrent')}</span>
                            <Input aria-label={t('concurrent')} type="number" min={0} step={1} className="h-9 w-28 rounded-xl" value={editing.concurrent} onChange={(event) => setEditing({ ...editing, concurrent: Math.max(0, Math.floor(Number(event.target.value) || 0)) })} />
                        </div>
                        <label className="flex h-9 cursor-pointer items-center gap-2 text-sm text-muted-foreground">
                            <input
                                type="checkbox"
                                checked={editing.enabled}
                                onChange={(event) => setEditing({ ...editing, enabled: event.target.checked })}
                            />
                            {t('form.enabled')}
                        </label>
                        <Button size="sm" onClick={save} disabled={savePolicy.isPending} className="rounded-xl">
                            {savePolicy.isPending && <Loader2 className="size-4 animate-spin" />}
                            {t('form.save')}
                        </Button>
                        <Button variant="ghost" size="sm" onClick={() => setEditing(null)} className="rounded-xl">{t('form.cancel')}</Button>
                    </div>
                    <p className="mt-2 text-xs text-muted-foreground">{t('form.hint')}</p>
                </div>
            )}

            <div className="min-h-0 flex-1 overflow-auto overscroll-contain rounded-3xl border border-border bg-card pb-24 md:pb-0">
                {isLoading ? (
                    <div className="flex h-full items-center justify-center gap-2 text-muted-foreground"><Loader2 className="size-4 animate-spin" />{t('loading')}</div>
                ) : isError ? (
                    <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground"><AlertCircle className="size-8" /><p>{t('error')}</p><Button variant="outline" size="sm" onClick={() => void refetch()} className="rounded-xl">{t('retry')}</Button></div>
                ) : policies.length === 0 ? (
                    <div className="flex h-full items-center justify-center text-muted-foreground">{t('empty')}</div>
                ) : (
                    <table className="w-full min-w-[720px] text-sm">
                        <thead className="sticky top-0 bg-card text-left text-xs text-muted-foreground"><tr>
                            <th className="px-4 py-3 font-medium">{t('table.scope')}</th><th className="px-4 py-3 font-medium">{t('table.model')}</th><th className="px-4 py-3 font-medium">{t('table.rpm')}</th><th className="px-4 py-3 font-medium">{t('table.tpm')}</th><th className="px-4 py-3 font-medium">{t('concurrent')}</th><th className="px-4 py-3 font-medium">{t('table.status')}</th><th className="px-4 py-3 font-medium">{t('table.updated')}</th><th className="px-4 py-3 font-medium">{t('table.actions')}</th>
                        </tr></thead>
                        <tbody>{policies.map((policy) => <tr key={policy.id} className="border-t border-border/60 hover:bg-muted/40">
                            <td className="whitespace-nowrap px-4 py-2.5">
                                <span className="mr-2 rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground">{t('scope.' + policy.scope_type)}</span>
                                {scopeLabel(policy.scope_type, policy.scope_id)}
                            </td>
                            <td className="whitespace-nowrap px-4 py-2.5 text-muted-foreground">{policy.model_name || t('table.allModels')}</td>
                            <td className="whitespace-nowrap px-4 py-2.5 tabular-nums">{policy.rpm || t('table.unlimited')}</td>
                            <td className="whitespace-nowrap px-4 py-2.5 tabular-nums">{policy.tpm ? formatTokens(policy.tpm) : t('table.unlimited')}</td>
                            <td className="whitespace-nowrap px-4 py-2.5">{policy.concurrent || t('table.unlimited')}</td>
 <td className="whitespace-nowrap px-4 py-2.5">{policy.enabled ? t('table.enabled') : t('table.disabled')}</td>
                            <td className="whitespace-nowrap px-4 py-2.5 tabular-nums text-muted-foreground">{formatTime(policy.updated_at)}</td>
                            <td className="whitespace-nowrap px-4 py-2.5">
                                <div className="flex gap-1">
                                    <Button variant="ghost" size="sm" onClick={() => startEdit(policy)} className="rounded-xl"><Pencil className="size-4" /></Button>
                                    <Button variant="ghost" size="sm" onClick={() => deletePolicy.mutate(policy.id)} className="rounded-xl text-destructive"><Trash2 className="size-4" /></Button>
                                </div>
                            </td>
                        </tr>)}</tbody>
                    </table>
                )}
            </div>

            <div className="shrink-0 rounded-3xl border border-border bg-card p-4">
                <div className="mb-3 flex flex-wrap items-center gap-2">
                    <h3 className="mr-2 font-semibold text-card-foreground">{t('inspect.title')}</h3>
                    <Select value={inspectScope.type} onValueChange={(value) => setInspectScope({ type: value as RateScopeType, id: 0, model: '' })}>
                        <SelectTrigger className="w-40 rounded-xl"><SelectValue /></SelectTrigger>
                        <SelectContent>
                            {SCOPE_TYPES.map((value) => <SelectItem key={value} value={value}>{t('scope.' + value)}</SelectItem>)}
                        </SelectContent>
                    </Select>
                    {inspectScope.type !== 'system' && (
                        <Input
                            className="h-9 w-28 rounded-xl"
                            placeholder={t('form.scopeIdHint')}
                            value={inspectScope.id ? String(inspectScope.id) : ''}
                            onChange={(event) => setInspectScope({ ...inspectScope, id: Number(event.target.value) || 0 })}
                        />
                    )}
                    <Input
                        className="h-9 w-40 rounded-xl"
                        placeholder={t('form.modelNameHint')}
                        value={inspectScope.model}
                        onChange={(event) => setInspectScope({ ...inspectScope, model: event.target.value })}
                    />
                </div>
                {inspect.data ? (
                    <div className="flex flex-wrap items-center gap-4 text-sm text-muted-foreground">
                        <span>{t('inspect.rpm')}: <b className="tabular-nums text-foreground">{inspect.data.rpm || t('table.unlimited')}</b></span>
                        <span>{t('inspect.tpm')}: <b className="tabular-nums text-foreground">{inspect.data.tpm ? formatTokens(inspect.data.tpm) : t('table.unlimited')}</b></span>
                        <span>{t('concurrent')}: <b>{inspect.data.concurrent || t('table.unlimited')}</b> / {t('active')}: {inspect.data.active}</span>
 <span>{t('inspect.requests')}: <b className="tabular-nums text-foreground">{formatTokens(inspect.data.requests)}</b></span>
                        <span>{t('inspect.tokens')}: <b className="tabular-nums text-foreground">{formatTokens(inspect.data.tokens)}</b></span>
                        <span>{t('inspect.resetAt')}: <b className="tabular-nums text-foreground">{formatTime(inspect.data.reset_at)}</b></span>
                        {inspect.data.blocked && <span className="rounded-full bg-destructive/10 px-2 py-0.5 text-destructive">{t('inspect.blocked')}</span>}
                    </div>
                ) : (
                    <div className="text-sm text-muted-foreground">{t('inspect.empty')}</div>
                )}
                {(inspect.data?.budgets?.length ?? 0) > 1 && <div className="mt-2 space-y-1 text-xs text-muted-foreground">{inspect.data?.budgets?.map(budget => <div key={budget.model_name}>{budget.model_name || t('table.allModels')}: RPM {budget.requests}/{budget.rpm || t('table.unlimited')} · TPM {budget.tokens}/{budget.tpm || t('table.unlimited')} · {t('active')} {budget.active}/{budget.concurrent || t('table.unlimited')}</div>)}</div>}
                {(usage.data?.items ?? []).length > 0 && (
                    <table className="mt-3 w-full min-w-[560px] text-sm">
                        <thead className="text-left text-xs text-muted-foreground"><tr>
                            <th className="py-2 font-medium">{t('usage.hour')}</th><th className="py-2 font-medium">{t('usage.requests')}</th><th className="py-2 font-medium">{t('usage.input')}</th><th className="py-2 font-medium">{t('usage.output')}</th><th className="py-2 font-medium">{t('usage.rejected')}</th>
                        </tr></thead>
                        <tbody>{(usage.data?.items ?? []).slice(0, 12).map((row) => <tr key={row.id} className="border-t border-border/60">
                            <td className="whitespace-nowrap py-2 tabular-nums text-muted-foreground">{formatTime(row.hour)}</td>
                            <td className="py-2 tabular-nums">{formatTokens(row.requests)}</td>
                            <td className="py-2 tabular-nums">{formatTokens(row.input_tokens)}</td>
                            <td className="py-2 tabular-nums">{formatTokens(row.output_tokens)}</td>
                            <td className={'py-2 tabular-nums' + (row.rejected > 0 ? ' text-destructive' : '')}>{formatTokens(row.rejected)}</td>
                        </tr>)}</tbody>
                    </table>
                )}
            </div>
        </div>
    );
}
