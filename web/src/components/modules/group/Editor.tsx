import { useCallback, useMemo, useState, type FormEvent } from 'react';
import { Check, ChevronDownIcon, HelpCircle, Plus, Search, Sparkles, Trash2 } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import * as AccordionPrimitive from '@radix-ui/react-accordion';
import { Protocol, useChannelGrantList } from '@/api/channel';
import { Button } from '@/components/ui/button';
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { Accordion, AccordionContent, AccordionItem } from '@/components/ui/accordion';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';
import { getModelIcon } from '@/lib/model-icons';
import type { GroupMode, GroupRelayConfig, PriceMetric } from '@/api/group';
import type { SelectedMember } from './ItemList';
import { MemberList } from './ItemList';
import { autoAddMatches, memberKey, normalizeKey } from './utils';

export type GroupEditorValues = {
    name: string;
    mode: GroupMode;
    relay_config: GroupRelayConfig;
    members: SelectedMember[];
};

// modeRelayDefaults 与后端 DefaultGroupRelayConfigForMode 保持同一套新建预设。
const modeRelayDefaults: Record<GroupMode, GroupRelayConfig> = {
    manual: { request_timeout_seconds: 600, max_wait_seconds: 30, max_waiting_requests: 0, max_total_attempts: 10, member_stream_idle_timeout_seconds: 60, member_max_attempts: 1, member_retry_interval_seconds: 1, member_non_stream_response_timeout_seconds: 120, member_stream_first_event_timeout_seconds: 30, member_cooldown_seconds: 60, member_affinity_seconds: 0, price_metric: 'blended', metric_window_size: 20, score_price_weight: 0, score_latency_weight: 0, score_success_weight: 0, reliability_floor: 0 },
    failover: { request_timeout_seconds: 600, max_wait_seconds: 30, max_waiting_requests: 0, max_total_attempts: 10, member_stream_idle_timeout_seconds: 60, member_max_attempts: 1, member_retry_interval_seconds: 1, member_non_stream_response_timeout_seconds: 120, member_stream_first_event_timeout_seconds: 30, member_cooldown_seconds: 60, member_affinity_seconds: 60, price_metric: 'blended', metric_window_size: 20, score_price_weight: 0, score_latency_weight: 0, score_success_weight: 0, reliability_floor: 0 },
    price: { request_timeout_seconds: 600, max_wait_seconds: 30, max_waiting_requests: 0, max_total_attempts: 10, member_stream_idle_timeout_seconds: 60, member_max_attempts: 1, member_retry_interval_seconds: 1, member_non_stream_response_timeout_seconds: 120, member_stream_first_event_timeout_seconds: 30, member_cooldown_seconds: 60, member_affinity_seconds: 0, price_metric: 'blended', metric_window_size: 20, score_price_weight: 0, score_latency_weight: 0, score_success_weight: 0, reliability_floor: 0 },
    latency: { request_timeout_seconds: 600, max_wait_seconds: 30, max_waiting_requests: 0, max_total_attempts: 10, member_stream_idle_timeout_seconds: 60, member_max_attempts: 1, member_retry_interval_seconds: 1, member_non_stream_response_timeout_seconds: 120, member_stream_first_event_timeout_seconds: 30, member_cooldown_seconds: 60, member_affinity_seconds: 0, price_metric: 'blended', metric_window_size: 10, score_price_weight: 0, score_latency_weight: 0, score_success_weight: 0, reliability_floor: 0 },
    success: { request_timeout_seconds: 600, max_wait_seconds: 30, max_waiting_requests: 0, max_total_attempts: 10, member_stream_idle_timeout_seconds: 60, member_max_attempts: 1, member_retry_interval_seconds: 1, member_non_stream_response_timeout_seconds: 120, member_stream_first_event_timeout_seconds: 30, member_cooldown_seconds: 60, member_affinity_seconds: 0, price_metric: 'blended', metric_window_size: 30, score_price_weight: 0, score_latency_weight: 0, score_success_weight: 0, reliability_floor: 0 },
    score: { request_timeout_seconds: 600, max_wait_seconds: 30, max_waiting_requests: 0, max_total_attempts: 10, member_stream_idle_timeout_seconds: 60, member_max_attempts: 1, member_retry_interval_seconds: 1, member_non_stream_response_timeout_seconds: 120, member_stream_first_event_timeout_seconds: 30, member_cooldown_seconds: 60, member_affinity_seconds: 0, price_metric: 'blended', metric_window_size: 20, score_price_weight: 0, score_latency_weight: 0, score_success_weight: 0, reliability_floor: 0 },
    random: { request_timeout_seconds: 600, max_wait_seconds: 30, max_waiting_requests: 0, max_total_attempts: 10, member_stream_idle_timeout_seconds: 60, member_max_attempts: 1, member_retry_interval_seconds: 1, member_non_stream_response_timeout_seconds: 120, member_stream_first_event_timeout_seconds: 30, member_cooldown_seconds: 30, member_affinity_seconds: 0, price_metric: 'blended', metric_window_size: 20, score_price_weight: 0, score_latency_weight: 0, score_success_weight: 0, reliability_floor: 0 },
};

function modeRelayConfig(mode: GroupMode): GroupRelayConfig {
    return { ...modeRelayDefaults[mode] };
}

// PROTOCOL_TAGS 是凭据行上的协议标识。
// 此处写全称: 凭据行只有名称一列, 横向有余量; 渠道表单的授权矩阵是三列复选框, 列宽紧张才用缩写。
const PROTOCOL_TAGS = [
    { bit: Protocol.OpenAIChatCompletion, label: 'Chat' },
    { bit: Protocol.OpenAIResponse, label: 'Response' },
    { bit: Protocol.AnthropicMessage, label: 'Message' },
];

// FieldHelp 渲染配置字段的简短帮助提示。
function FieldHelp({ text }: { text: string }) {
    return (
        <Tooltip>
            <TooltipTrigger asChild>
                <HelpCircle className="size-4 cursor-help text-muted-foreground" />
            </TooltipTrigger>
            <TooltipContent side="top" sideOffset={10} align="center">
                {text}
            </TooltipContent>
        </Tooltip>
    );
}

function ModelPickerSection({
    grantMembers,
    selectedMembers,
    onAdd,
    onAutoAdd,
    autoAddDisabled,
    searchKeyword,
    onSearchChange,
    autoAddTitle,
}: {
    grantMembers: SelectedMember[];
    selectedMembers: SelectedMember[];
    onAdd: (channel: SelectedMember) => void;
    onAutoAdd: () => void;
    autoAddDisabled: boolean;
    searchKeyword: string;
    onSearchChange: (value: string) => void;
    autoAddTitle: string;
}) {
    const t = useTranslations('group');
    const setSearchKeyword = onSearchChange;

    const selectedKeys = useMemo(() => new Set(selectedMembers.map(memberKey)), [selectedMembers]);
    const normalizedSearch = searchKeyword.trim().toLowerCase();

    // 候选按渠道 -> 模型 -> 凭据三级组织: 一个模型可有多份凭据, 各自是独立授权, 需再展开一级才能分别选取。
    // 三级顺序沿用后端给出的候选顺序: Map 保留插入顺序, 后端已按渠道, 模型, 凭据排好, 此处无需再排。
    const channels = useMemo(() => {
        const byChannel = new Map<number, {
            id: number;
            name: string;
            models: Map<string, SelectedMember[]>;
        }>();
        grantMembers.forEach((mc) => {
            let channel = byChannel.get(mc.channel_id);
            if (!channel) {
                channel = { id: mc.channel_id, name: mc.channel_name, models: new Map() };
                byChannel.set(mc.channel_id, channel);
            }
            const grants = channel.models.get(mc.name);
            if (grants) grants.push(mc);
            else channel.models.set(mc.name, [mc]);
        });

        return Array.from(byChannel.values()).map((channel) => ({
            id: channel.id,
            name: channel.name,
            models: Array.from(channel.models, ([name, grants]) => ({ name, grants })),
        }));
    }, [grantMembers]);

    const filteredChannels = useMemo(() => {
        if (!normalizedSearch) return channels;
        return channels.reduce<typeof channels>((acc, channel) => {
            if (channel.name.toLowerCase().includes(normalizedSearch)) {
                acc.push(channel);
                return acc;
            }

            const models = channel.models.filter((model) => model.name.toLowerCase().includes(normalizedSearch));
            if (models.length > 0) acc.push({ ...channel, models });
            return acc;
        }, []);
    }, [channels, normalizedSearch]);

    return (
        <div className="rounded-xl border border-border/50 bg-muted/30 flex flex-col min-h-0">
            <div className="grid grid-cols-[1fr_auto_1fr] items-center gap-2 px-3 py-2 border-b border-border/30 bg-muted/50">
                <span className="min-w-0 justify-self-start text-sm font-medium text-foreground">
                    {t('form.addItem')}
                </span>

                <div className="relative justify-self-center w-30">
                    <Search className="pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
                    <Input
                        value={searchKeyword}
                        onChange={(event) => setSearchKeyword(event.target.value)}
                        className="h-6 rounded-lg border-border/60 bg-background/70 pl-7 pr-2 text-xs shadow-none focus-visible:border-border/60 focus-visible:ring-0"
                        aria-label="search"
                    />
                </div>

                <button
                    type="button"
                    onClick={onAutoAdd}
                    title={autoAddTitle}
                    className={cn(
                        'justify-self-end shrink-0 flex items-center gap-1 px-2 py-1 rounded-lg text-xs font-medium transition-colors',
                        autoAddDisabled
                            ? 'text-muted-foreground/50 cursor-not-allowed'
                            : 'hover:bg-muted text-muted-foreground hover:text-foreground'
                    )}
                    disabled={autoAddDisabled}
                >
                    <Sparkles className="size-3.5" />
                    <span>{t('form.autoAdd')}</span>
                </button>
            </div>

            <div className="flex-1 min-h-0 overflow-y-auto p-2">
                <Accordion type="multiple" className="w-full space-y-2">
                    {filteredChannels.map((channel) => {
                        // 计数按授权算而非按模型: 展开后每份凭据都是一个可选项。
                        const grants = channel.models.flatMap((model) => model.grants);
                        const total = grants.length;
                        const selectedCount = grants.reduce(
                            (acc, m) => acc + (selectedKeys.has(memberKey(m)) ? 1 : 0),
                            0
                        );
                        const available = total - selectedCount;

                        return (
                            <AccordionItem key={channel.id} value={`channel-${channel.id}`}>
                                <AccordionPrimitive.Header className="rounded-lg bg-muted sticky top-0 z-10 flex px-2 overflow-hidden">
                                    <AccordionPrimitive.Trigger className="flex flex-1 min-w-0 items-center gap-4 py-4 text-left text-sm transition-all outline-none focus-visible:ring-[3px] disabled:pointer-events-none disabled:opacity-50 [&[data-state=open]>svg]:rotate-180">
                                        <span className="truncate">{channel.name}</span>
                                        <span className="text-xs text-muted-foreground shrink-0">
                                            {available}/{total}
                                        </span>
                                        <ChevronDownIcon className="text-muted-foreground pointer-events-none size-4 shrink-0 transition-transform duration-200" />
                                    </AccordionPrimitive.Trigger>
                                </AccordionPrimitive.Header>
                                <AccordionContent className="px-2 pt-2">
                                    <div className="flex flex-col gap-1.5">
                                        {channel.models.map((model) => {
                                            const { Icon, className: iconClassName } = getModelIcon(model.name);
                                            const modelSelected = model.grants.reduce(
                                                (acc, m) => acc + (selectedKeys.has(memberKey(m)) ? 1 : 0),
                                                0
                                            );
                                            // 对客价: 供货价经系统上浮后的价格, 与计费同口径; 同一模型各授权价格一致, 取第一条展示。
                                            const userPrice = model.grants.find((m) => m.user_price)?.user_price;
                                            return (
                                                <div key={model.name} className="rounded-lg border border-border/50 bg-background">
                                                    {/* 模型行只作分组标题, 不可点选: 可选的是它下面的凭据, 一份凭据一条授权。 */}
                                                    <div className="flex items-center justify-between gap-2 px-2.5 py-2">
                                                        <span className="flex items-center gap-2 min-w-0">
                                                            <Icon aria-hidden="true" className={iconClassName} width={16} height={16} />
                                                            <span className="text-sm font-medium truncate">{model.name}</span>
                                                        </span>
                                                        <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
                                                            {model.grants.length - modelSelected}/{model.grants.length}
                                                        </span>
                                                    </div>
                                                    {userPrice && (
                                                        <p className="px-2.5 pb-2 -mt-1 text-[10px] text-muted-foreground tabular-nums">
                                                            {t('modelPrice', {
                                                                input: userPrice.input.toFixed(6),
                                                                output: userPrice.output.toFixed(6),
                                                                cacheRead: userPrice.cache_read.toFixed(6),
                                                                cacheWrite: userPrice.cache_write.toFixed(6),
                                                            })}
                                                        </p>
                                                    )}

                                                    <div className="flex flex-col border-t border-border/50">
                                                        {model.grants.map((m) => {
                                                            const isSelected = selectedKeys.has(memberKey(m));
                                                            return (
                                                                <button
                                                                    key={memberKey(m)}
                                                                    type="button"
                                                                    onClick={() => !isSelected && onAdd(m)}
                                                                    disabled={isSelected}
                                                                    className={cn(
                                                                        'flex w-full items-center justify-between gap-2 px-2.5 py-1.5 pl-8 text-left transition-colors',
                                                                        isSelected ? 'opacity-60 cursor-not-allowed' : 'hover:bg-muted'
                                                                    )}
                                                                >
                                                                    <span className="flex min-w-0 items-center gap-2">
                                                                        <span className="truncate text-xs text-muted-foreground">{m.key_name}</span>
                                                                        {/* 标出该凭据讲得通的协议: 同一模型的不同凭据可能只支持其中一部分, 选之前就要能看出来。 */}
                                                                        {PROTOCOL_TAGS.map(({ bit, label }) => (m.protocols & bit) !== 0 && (
                                                                            <span
                                                                                key={bit}
                                                                                className="shrink-0 rounded border border-border/60 px-1 text-[10px] leading-4 text-muted-foreground"
                                                                            >
                                                                                {label}
                                                                            </span>
                                                                        ))}
                                                                    </span>
                                                                    <span className="shrink-0 text-muted-foreground">
                                                                        {isSelected ? (
                                                                            <Check className="size-4 text-primary" />
                                                                        ) : (
                                                                            <Plus className="size-4" />
                                                                        )}
                                                                    </span>
                                                                </button>
                                                            );
                                                        })}
                                                    </div>
                                                </div>
                                            );
                                        })}
                                    </div>
                                </AccordionContent>
                            </AccordionItem>
                        );
                    })}
                </Accordion>
            </div>
        </div>
    );
}

function SortSection({
    members,
    onReorder,
    onRemove,
    removingIds,
    onClear,
}: {
    members: SelectedMember[];
    onReorder: (members: SelectedMember[]) => void;
    onRemove: (id: string) => void;
    removingIds: Set<string>;
    onClear: () => void;
}) {
    const t = useTranslations('group');

    return (
        <div className="rounded-xl border border-border/50 bg-muted/30 flex flex-col min-h-0">
            <div className="flex items-center justify-between px-3 py-2 border-b border-border/30 bg-muted/50">
                <span className="text-sm font-medium text-foreground">
                    {t('form.items')}
                    {members.length > 0 && (
                        <span className="ml-1.5 text-xs text-muted-foreground font-normal">
                            ({members.length})
                        </span>
                    )}
                </span>
                <button
                    type="button"
                    onClick={onClear}
                    disabled={members.length === 0}
                    className={cn(
                        'flex items-center gap-1 px-2 py-1 rounded-lg text-xs font-medium transition-colors',
                        members.length === 0
                            ? 'text-muted-foreground/50 cursor-not-allowed'
                            : 'hover:bg-muted text-muted-foreground hover:text-foreground'
                    )}
                >
                    <Trash2 className="size-3.5" />
                    <span>{t('form.clear')}</span>
                </button>
            </div>

            <div className="flex-1 min-h-0">
                <MemberList
                    members={members}
                    onReorder={onReorder}
                    onRemove={onRemove}
                    removingIds={removingIds}
                    showConfirmDelete={false}
                />
            </div>
        </div>
    );
}

export function GroupEditor({
    initial,
    submitText,
    submittingText,
    isSubmitting,
    onSubmit,
    onCancel,
}: {
    initial?: {
        name?: string;
        mode?: GroupMode;
        relay_config?: Partial<GroupRelayConfig>;
        members?: SelectedMember[];
    };
    submitText: string;
    submittingText: string;
    isSubmitting: boolean;
    onSubmit: (values: GroupEditorValues) => void;
    onCancel?: () => void;
}) {
    const t = useTranslations('group');
    const { data: grantCandidates = [] } = useChannelGrantList();
    const grantMembers = useMemo<SelectedMember[]>(() => grantCandidates.map((grant) => ({
        id: String(grant.id),
        channel_grant_id: grant.id,
        name: grant.model_name,
        enabled: grant.available,
        channel_id: grant.channel_id,
        channel_name: grant.channel_name,
        key_name: grant.key_name,
        protocols: grant.protocols,
        user_price: grant.user_price,
    })), [grantCandidates]);

    const [groupName, setGroupName] = useState(initial?.name ?? '');
    const [mode, setMode] = useState<GroupMode>(initial?.mode ?? 'manual');
    const [relayConfig, setRelayConfig] = useState<GroupRelayConfig>(() => ({
        ...modeRelayConfig(initial?.mode ?? 'manual'),
        ...initial?.relay_config,
    }));
    const [selectedMembers, setSelectedMembers] = useState<SelectedMember[]>(initial?.members ?? []);
    const [removingIds, setRemovingIds] = useState<Set<string>>(new Set());

    const groupKey = normalizeKey(groupName);
    // 搜索关键字提升为编辑器状态: 自动添加与左侧列表筛选共用同一份, 所见即所得。
    const [searchKeyword, setSearchKeyword] = useState('');

    // 自动添加候选(优先级链见 utils.autoAddMatches): 关键字优先, 组名回退, 皆空为无。
    const matchedModelChannels = useMemo(() => {
        if (!searchKeyword.trim() && !groupKey) return [];
        return grantMembers.filter((mc) => autoAddMatches(mc, searchKeyword, groupKey));
    }, [groupKey, searchKeyword, grantMembers]);
    const autoAddCandidates = useMemo(
        () => matchedModelChannels.filter((mc) => !selectedMembers.some((m) => memberKey(m) === memberKey(mc))),
        [matchedModelChannels, selectedMembers],
    );
    // 置灰原因分三档, 随 title 提示, 让"为什么点不了"自解释。
    const autoAddTitle = useMemo(() => {
        if (!searchKeyword.trim() && !groupKey) return t('form.autoAddNeedKeyword');
        if (matchedModelChannels.length === 0) return t('form.autoAddNoMatch');
        if (autoAddCandidates.length === 0) return t('form.autoAddAllAdded');
        return t('form.autoAddTitle', { count: autoAddCandidates.length });
    }, [t, searchKeyword, groupKey, matchedModelChannels.length, autoAddCandidates.length]);

    const handleAddMember = useCallback((channel: SelectedMember) => {
        const key = memberKey(channel);
        setSelectedMembers((prev) => {
            if (prev.some((m) => m.id === key)) return prev;
            return [...prev, { ...channel, id: key }];
        });
    }, []);

    const autoAddDisabled = autoAddCandidates.length === 0;

    // 只增不减: 候选追加到末尾, 已有成员及其顺序一律不动; 按 channel_grant_id 去重。
    const handleAutoAdd = useCallback(() => {
        if (autoAddCandidates.length === 0) return;
        const toAdd = autoAddCandidates.map((mc) => ({ ...mc, id: memberKey(mc) }));
        setSelectedMembers((prev) => [...prev, ...toAdd]);
        toast.success(t('form.autoAddDone', { count: toAdd.length }));
    }, [autoAddCandidates, t]);

    const handleRemoveMember = useCallback((id: string) => {
        setRemovingIds((prev) => new Set(prev).add(id));
        setTimeout(() => {
            setSelectedMembers((prev) => prev.filter((m) => m.id !== id));
            setRemovingIds((prev) => { const n = new Set(prev); n.delete(id); return n; });
        }, 200);
    }, []);

    const handleClearMembers = useCallback(() => {
        setSelectedMembers([]);
        setRemovingIds(new Set());
    }, []);

    const isValid = groupKey.length > 0 && selectedMembers.length > 0;

    const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        if (!isValid) return;
        onSubmit({
            name: groupName,
            mode,
            relay_config: relayConfig,
            members: selectedMembers,
        });
    };


    return (
        <form onSubmit={handleSubmit} className="flex flex-col h-full min-h-0 ">
            <div className="flex-1 min-h-0 overflow-hidden px-1">
                <FieldGroup className="gap-4 flex flex-col min-h-0 h-full">
                    <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                        <Field>
                            <FieldLabel htmlFor="group-name">{t('form.name')}</FieldLabel>
                            <Input
                                id="group-name"
                                value={groupName}
                                onChange={(e) => setGroupName(e.target.value)}
                                className="rounded-xl"
                            />
                        </Field>
                        <Field>
                            <FieldLabel htmlFor="group-mode">
                                {t('form.mode')}
                                <FieldHelp text={t('form.modeHint')} />
                            </FieldLabel>
                            <Select
                                value={mode}
                                onValueChange={(value) => {
                                    const nextMode = value as GroupMode;
                                    setMode(nextMode);
                                    // 新建分组切换策略时直接采用该策略预设; 编辑已有分组保留手工调整值。
                                    if (!initial) setRelayConfig(modeRelayConfig(nextMode));
                                }}
                            >
                                <SelectTrigger id="group-mode" className="w-full rounded-xl">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="manual">{t('form.manual')}</SelectItem>
                                    <SelectItem value="failover">{t('form.failover')}</SelectItem>
                                    <SelectItem value="price">{t('form.price')}</SelectItem>
                                    <SelectItem value="latency">{t('form.latency')}</SelectItem>
                                    <SelectItem value="success">{t('form.success')}</SelectItem>
                                    <SelectItem value="score">{t('form.score')}</SelectItem>
                                    <SelectItem value="random">{t('form.random')}</SelectItem>
                                </SelectContent>
                            </Select>
                        </Field>
                    </div>

                    <Tabs defaultValue="members" className="flex flex-1 min-h-0">
                        <TabsList className="grid w-full shrink-0 grid-cols-2">
                            <TabsTrigger value="members">{t('form.members')}</TabsTrigger>
                            <TabsTrigger value="relay">{t('form.relay')}</TabsTrigger>
                        </TabsList>

                        <TabsContent value="members" className="min-h-0 overflow-hidden">
                            <div className="grid h-full min-h-0 grid-cols-1 gap-4 md:grid-cols-2">
                                <ModelPickerSection
                                    grantMembers={grantMembers}
                                    selectedMembers={selectedMembers}
                                    onAdd={handleAddMember}
                                    onAutoAdd={handleAutoAdd}
                                    autoAddDisabled={autoAddDisabled}
                                    searchKeyword={searchKeyword}
                                    onSearchChange={setSearchKeyword}
                                    autoAddTitle={autoAddTitle}
                                />
                                <SortSection
                                    members={selectedMembers}
                                    onReorder={setSelectedMembers}
                                    onRemove={handleRemoveMember}
                                    removingIds={removingIds}
                                    onClear={handleClearMembers}
                                />
                            </div>
                        </TabsContent>

                        <TabsContent value="relay" className="min-h-0 overflow-y-auto px-1">
                            <div className="mb-3 flex items-center justify-between gap-2 rounded-xl border border-border/50 bg-muted/30 px-3 py-2">
                                <span className="text-xs text-muted-foreground">{t('form.presetHint')}</span>
                                <Button type="button" variant="outline" size="sm" className="rounded-lg" onClick={() => setRelayConfig(modeRelayConfig(mode))}>{t('form.applyPreset')}</Button>
                            </div>
                            <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                                {(['request_timeout_seconds', 'max_total_attempts', 'max_wait_seconds', 'max_waiting_requests', 'member_stream_idle_timeout_seconds'] as const).map((key) => (
                                    <Field key={key}>
                                        <FieldLabel htmlFor={'group-' + key}>{t('form.' + key)}<FieldHelp text={t('form.' + key + 'Hint')} /></FieldLabel>
                                        <Input id={'group-' + key} type="number" min={key.startsWith('max_wait') ? 0 : 1} step={1} className="rounded-xl" value={relayConfig[key]} onChange={(event) => setRelayConfig(prev => ({...prev, [key]: Math.max(key.startsWith('max_wait') ? 0 : 1, Math.floor(Number(event.target.value) || 0))}))} />
                                    </Field>
                                ))}
                                {mode !== 'manual' && (<>
                                <Field>
                                    <FieldLabel htmlFor="group-retry-count">
                                        {t('form.retryCount')}
                                        <FieldHelp text={t('form.retryCountHint')} />
                                    </FieldLabel>
                                    <Input
                                        id="group-retry-count"
                                        type="number"
                                        inputMode="numeric"
                                        min={0}
                                        step={1}
                                        value={String(relayConfig.member_max_attempts)}
                                        onChange={(event) => {
                                            const value = Number.parseInt(event.target.value, 10);
                                            setRelayConfig((prev) => ({ ...prev, member_max_attempts: Number.isFinite(value) && value >= 1 ? value : 1 }));
                                        }}
                                        className="rounded-xl"
                                    />
                                </Field>
                                </>)}
                                <Field>
                                    <FieldLabel htmlFor="group-retry-interval">
                                        {t('form.retryInterval')}
                                        <FieldHelp text={t('form.retryIntervalHint')} />
                                    </FieldLabel>
                                    <Input
                                        id="group-retry-interval"
                                        type="number"
                                        inputMode="numeric"
                                        min={1}
                                        step={1}
                                        value={String(relayConfig.member_retry_interval_seconds)}
                                        onChange={(event) => {
                                            const value = Number.parseInt(event.target.value, 10);
                                            setRelayConfig((prev) => ({ ...prev, member_retry_interval_seconds: Number.isFinite(value) && value >= 1 ? value : 1 }));
                                        }}
                                        className="rounded-xl"
                                    />
                                </Field>
                                <Field>
                                    <FieldLabel htmlFor="group-non-stream-timeout">
                                        {t('form.nonStreamTimeout')}
                                        <FieldHelp text={t('form.nonStreamTimeoutHint')} />
                                    </FieldLabel>
                                    <Input
                                        id="group-non-stream-timeout"
                                        type="number"
                                        inputMode="numeric"
                                        min={1}
                                        step={1}
                                        value={String(relayConfig.member_non_stream_response_timeout_seconds)}
                                        onChange={(event) => {
                                            const value = Number.parseInt(event.target.value, 10);
                                            setRelayConfig((prev) => ({ ...prev, member_non_stream_response_timeout_seconds: Number.isFinite(value) && value >= 1 ? value : 1 }));
                                        }}
                                        className="rounded-xl"
                                    />
                                </Field>
                                <Field>
                                    <FieldLabel htmlFor="group-stream-timeout">
                                        {t('form.streamTimeout')}
                                        <FieldHelp text={t('form.streamTimeoutHint')} />
                                    </FieldLabel>
                                    <Input
                                        id="group-stream-timeout"
                                        type="number"
                                        inputMode="numeric"
                                        min={1}
                                        step={1}
                                        value={String(relayConfig.member_stream_first_event_timeout_seconds)}
                                        onChange={(event) => {
                                            const value = Number.parseInt(event.target.value, 10);
                                            setRelayConfig((prev) => ({ ...prev, member_stream_first_event_timeout_seconds: Number.isFinite(value) && value >= 1 ? value : 1 }));
                                        }}
                                        className="rounded-xl"
                                    />
                                </Field>
                                {mode !== 'manual' && (<>
                                <Field>
                                    <FieldLabel htmlFor="group-cooldown">
                                        {t('form.cooldown')}
                                        <FieldHelp text={t('form.cooldownHint')} />
                                    </FieldLabel>
                                    <Input
                                        id="group-cooldown"
                                        type="number"
                                        inputMode="numeric"
                                        min={1}
                                        step={1}
                                        value={String(relayConfig.member_cooldown_seconds)}
                                        onChange={(event) => {
                                            const value = Number.parseInt(event.target.value, 10);
                                            setRelayConfig((prev) => ({ ...prev, member_cooldown_seconds: Number.isFinite(value) && value >= 1 ? value : 1 }));
                                        }}
                                        className="rounded-xl"
                                    />
                                </Field>
                                <Field>
                                    <FieldLabel htmlFor="group-affinity">
                                        {t('form.affinity')}
                                        <FieldHelp text={t('form.affinityHint')} />
                                    </FieldLabel>
                                    <Input
                                        id="group-affinity"
                                        type="number"
                                        inputMode="numeric"
                                        min={0}
                                        step={1}
                                        value={String(relayConfig.member_affinity_seconds)}
                                        onChange={(event) => {
                                            const value = Number.parseInt(event.target.value, 10);
                                            setRelayConfig((prev) => ({ ...prev, member_affinity_seconds: Number.isFinite(value) && value >= 0 ? value : 0 }));
                                        }}
                                        className="rounded-xl"
                                    />
                                </Field>
                                </>)}
                                {(mode === 'price' || mode === 'score') && (
                                <Field>
                                    <FieldLabel htmlFor="group-price-metric">
                                        {t('form.priceMetric')}
                                        <FieldHelp text={t('form.priceMetricHint')} />
                                    </FieldLabel>
                                    <Select
                                        value={relayConfig.price_metric}
                                        onValueChange={(value) => setRelayConfig((prev) => ({ ...prev, price_metric: value as PriceMetric }))}
                                    >
                                        <SelectTrigger id="group-price-metric" className="w-full rounded-xl">
                                            <SelectValue />
                                        </SelectTrigger>
                                        <SelectContent>
                                            <SelectItem value="blended">{t('form.priceMetricBlended')}</SelectItem>
                                            <SelectItem value="input">{t('form.priceMetricInput')}</SelectItem>
                                            <SelectItem value="output">{t('form.priceMetricOutput')}</SelectItem>
                                        </SelectContent>
                                    </Select>
                                </Field>
                                )}
                                {(mode === 'latency' || mode === 'success' || mode === 'score') && (
                                <Field>
                                    <FieldLabel htmlFor="group-metric-window">
                                        {t('form.metricWindow')}
                                        <FieldHelp text={t('form.metricWindowHint')} />
                                    </FieldLabel>
                                    <Input
                                        id="group-metric-window"
                                        type="number"
                                        inputMode="numeric"
                                        min={5}
                                        max={200}
                                        step={1}
                                        value={String(relayConfig.metric_window_size)}
                                        onChange={(event) => {
                                            const value = Number.parseInt(event.target.value, 10);
                                            setRelayConfig((prev) => ({ ...prev, metric_window_size: Number.isFinite(value) && value >= 5 && value <= 200 ? value : 20 }));
                                        }}
                                        className="rounded-xl"
                                    />
                                </Field>
                                )}
                                {mode === 'score' && (
                                <>
                                <Field>
                                    <FieldLabel htmlFor="group-score-price-weight">
                                        {t('form.scorePriceWeight')}
                                        <FieldHelp text={t('form.scoreWeightHint')} />
                                    </FieldLabel>
                                    <Input
                                        id="group-score-price-weight"
                                        type="number"
                                        inputMode="numeric"
                                        min={0}
                                        max={100}
                                        step={1}
                                        value={String(relayConfig.score_price_weight)}
                                        onChange={(event) => {
                                            const value = Number.parseInt(event.target.value, 10);
                                            setRelayConfig((prev) => ({ ...prev, score_price_weight: Number.isFinite(value) ? Math.min(Math.max(value, 0), 100) : 0 }));
                                        }}
                                        className="rounded-xl"
                                    />
                                </Field>
                                <Field>
                                    <FieldLabel htmlFor="group-score-latency-weight">
                                        {t('form.scoreLatencyWeight')}
                                        <FieldHelp text={t('form.scoreWeightHint')} />
                                    </FieldLabel>
                                    <Input
                                        id="group-score-latency-weight"
                                        type="number"
                                        inputMode="numeric"
                                        min={0}
                                        max={100}
                                        step={1}
                                        value={String(relayConfig.score_latency_weight)}
                                        onChange={(event) => {
                                            const value = Number.parseInt(event.target.value, 10);
                                            setRelayConfig((prev) => ({ ...prev, score_latency_weight: Number.isFinite(value) ? Math.min(Math.max(value, 0), 100) : 0 }));
                                        }}
                                        className="rounded-xl"
                                    />
                                </Field>
                                <Field>
                                    <FieldLabel htmlFor="group-score-success-weight">
                                        {t('form.scoreSuccessWeight')}
                                        <FieldHelp text={t('form.scoreWeightHint')} />
                                    </FieldLabel>
                                    <Input
                                        id="group-score-success-weight"
                                        type="number"
                                        inputMode="numeric"
                                        min={0}
                                        max={100}
                                        step={1}
                                        value={String(relayConfig.score_success_weight)}
                                        onChange={(event) => {
                                            const value = Number.parseInt(event.target.value, 10);
                                            setRelayConfig((prev) => ({ ...prev, score_success_weight: Number.isFinite(value) ? Math.min(Math.max(value, 0), 100) : 0 }));
                                        }}
                                        className="rounded-xl"
                                    />
                                </Field>
                                <Field>
                                    <FieldLabel htmlFor="group-reliability-floor">
                                        {t('form.reliabilityFloor')}
                                        <FieldHelp text={t('form.reliabilityFloorHint')} />
                                    </FieldLabel>
                                    <Input
                                        id="group-reliability-floor"
                                        type="number"
                                        inputMode="numeric"
                                        min={0}
                                        max={100}
                                        step={1}
                                        value={String(relayConfig.reliability_floor ?? 0)}
                                        onChange={(event) => {
                                            const value = Number.parseInt(event.target.value, 10);
                                            setRelayConfig((prev) => ({ ...prev, reliability_floor: Number.isFinite(value) ? Math.min(Math.max(value, 0), 100) : 0 }));
                                        }}
                                        className="rounded-xl"
                                    />
                                </Field>
                                </>
                                )}
                            </div>
                        </TabsContent>
                    </Tabs>
                </FieldGroup>
            </div>

            <div className="pt-4 mt-auto shrink-0">
                <div className="flex gap-2">
                    {onCancel && (
                        <Button type="button" variant="secondary" className="flex-1 rounded-xl h-11" onClick={onCancel}>
                            {t('detail.actions.cancel')}
                        </Button>
                    )}
                    <Button
                        type="submit"
                        disabled={!isValid || isSubmitting}
                        className="flex-1 rounded-xl h-11"
                    >
                        {isSubmitting ? submittingText : submitText}
                    </Button>
                </div>
            </div>
        </form>
    );
}
