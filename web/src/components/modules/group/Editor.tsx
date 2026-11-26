import { useCallback, useMemo, useState, type FormEvent, type MouseEvent } from 'react';
import { Check, ChevronDownIcon, HelpCircle, LoaderCircle, Plus, Search, Sparkles, Trash2 } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import * as AccordionPrimitive from '@radix-ui/react-accordion';
import { Protocol, useChannelGrantList } from '@/api/channel';
import { Button } from '@/components/ui/button';
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { Checkbox } from '@/components/ui/checkbox';
import { useProbeModelToolCapability } from '@/api/model';
import { Accordion, AccordionContent, AccordionItem } from '@/components/ui/accordion';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';
import { getModelIcon } from '@/lib/model-icons';
import type { GroupMode, GroupRelayConfig, PriceMetric } from '@/api/group';
import type { SelectedMember } from './ItemList';
import { MemberList } from './ItemList';
import { autoAddMatches, memberKey, normalizeKey } from './utils';
import { canaryDefaults, canaryOf, canarySubmitBlocked, canarySubmitConfig, formatCanaryUserIDs, parseCanaryUserIDs, preserveCanary, type CanaryConfig } from './canary';
import { autoGrantIDsValue, contextOverridesValue, parseAutoGrantIDs, parseContextOverrides, toggleAutoGrantID, type AutoTaskConfigField } from './autoRouting';

export type GroupEditorValues = {
    name: string;
    mode: GroupMode;
    relay_config: GroupRelayConfig;
    members: SelectedMember[];
};

function ToolProbeRunButton({ label, pending, onClick }: {
    label: string; pending: boolean; onClick: (event: MouseEvent<HTMLButtonElement>) => void;
}) {
    return (
        <button
            type="button"
            disabled={pending}
            aria-label={label}
            onClick={onClick}
            className="min-h-7 max-w-full whitespace-normal rounded-md border border-border px-2 py-1 text-xs text-muted-foreground hover:bg-muted disabled:opacity-50"
        >
            {pending ? <LoaderCircle className="size-3 animate-spin" /> : label}
        </button>
    );
}

function ToolProbeControls({ grantID, score, stream, onAnthropicVerified }: {
    grantID: number;
    score: number;
    stream?: boolean;
    onAnthropicVerified?: () => void;
}) {
    const t = useTranslations('group.form');
    const probe = useProbeModelToolCapability();
    return (
        <div className="mt-2 flex flex-wrap items-center gap-2 whitespace-normal" onClick={(event) => event.stopPropagation()}>
            <ToolProbeRunButton
                label={stream ? t('probeAnthropicStream') : onAnthropicVerified ? t('probeAnthropicTool') : t('probeChatTool')}
                pending={probe.isPending}
                onClick={(event) => {
                    event.preventDefault();
                    event.stopPropagation();
                    probe.mutate({ channel_grant_id: grantID, score, stream, evaluate: stream, protocol: onAnthropicVerified ? 'anthropic_messages' : 'openai_chat' }, {
                        onSuccess: (result) => {
                            if (!result.success) {
                                toast.error(result.error || t('probeFailed'));
                                return;
                            }
                            onAnthropicVerified?.();
                            toast.success(onAnthropicVerified ? t('probeAnthropicPassed') : t('probeChatPassed'));
                        },
                        onError: (error) => toast.error(error.message),
                    });
                }}
            />
            {probe.data?.evaluation?.map((result) => (
                <span key={result.task} className="text-xs text-muted-foreground">
                    {t(result.task === 'coding' ? 'probeCodingResult' : 'probeToolResult', {
                        passed: result.passed,
                        total: result.total,
                        ms: result.duration_ms,
                    })}
                    {' · '}
                    {result.estimated_supply_cost == null
                        ? t('probeCostUnavailable')
                        : t('probeCost', { cost: result.estimated_supply_cost.toPrecision(3), currency: result.currency || 'USD' })}
                </span>
            ))}
        </div>
    );
}

// ModeRelayDefaults 是各模式预设里与模式相关的部分: 灰度四项不随模式变化, 由 modeRelayConfig 统一补齐。
type ModeRelayDefaults = Omit<GroupRelayConfig, keyof CanaryConfig>;

// modeRelayDefaults 与后端 DefaultGroupRelayConfigForMode 保持同一套新建预设。
const modeRelayDefaults: Record<GroupMode, ModeRelayDefaults> = {
    manual: { request_timeout_seconds: 600, max_wait_seconds: 30, max_waiting_requests: 0, max_total_attempts: 10, member_stream_idle_timeout_seconds: 60, member_max_attempts: 1, member_retry_interval_seconds: 1, member_non_stream_response_timeout_seconds: 120, member_stream_first_event_timeout_seconds: 30, member_cooldown_seconds: 60, member_affinity_seconds: 0, price_metric: 'blended', metric_window_size: 20, score_price_weight: 0, score_latency_weight: 0, score_success_weight: 0, reliability_floor: 0 },
    failover: { request_timeout_seconds: 600, max_wait_seconds: 30, max_waiting_requests: 0, max_total_attempts: 10, member_stream_idle_timeout_seconds: 60, member_max_attempts: 1, member_retry_interval_seconds: 1, member_non_stream_response_timeout_seconds: 120, member_stream_first_event_timeout_seconds: 30, member_cooldown_seconds: 60, member_affinity_seconds: 60, price_metric: 'blended', metric_window_size: 20, score_price_weight: 0, score_latency_weight: 0, score_success_weight: 0, reliability_floor: 0 },
    price: { request_timeout_seconds: 600, max_wait_seconds: 30, max_waiting_requests: 0, max_total_attempts: 10, member_stream_idle_timeout_seconds: 60, member_max_attempts: 1, member_retry_interval_seconds: 1, member_non_stream_response_timeout_seconds: 120, member_stream_first_event_timeout_seconds: 30, member_cooldown_seconds: 60, member_affinity_seconds: 0, price_metric: 'blended', metric_window_size: 20, score_price_weight: 0, score_latency_weight: 0, score_success_weight: 0, reliability_floor: 0 },
    latency: { request_timeout_seconds: 600, max_wait_seconds: 30, max_waiting_requests: 0, max_total_attempts: 10, member_stream_idle_timeout_seconds: 60, member_max_attempts: 1, member_retry_interval_seconds: 1, member_non_stream_response_timeout_seconds: 120, member_stream_first_event_timeout_seconds: 30, member_cooldown_seconds: 60, member_affinity_seconds: 0, price_metric: 'blended', metric_window_size: 10, score_price_weight: 0, score_latency_weight: 0, score_success_weight: 0, reliability_floor: 0 },
    success: { request_timeout_seconds: 600, max_wait_seconds: 30, max_waiting_requests: 0, max_total_attempts: 10, member_stream_idle_timeout_seconds: 60, member_max_attempts: 1, member_retry_interval_seconds: 1, member_non_stream_response_timeout_seconds: 120, member_stream_first_event_timeout_seconds: 30, member_cooldown_seconds: 60, member_affinity_seconds: 0, price_metric: 'blended', metric_window_size: 30, score_price_weight: 0, score_latency_weight: 0, score_success_weight: 0, reliability_floor: 0 },
    score: { request_timeout_seconds: 600, max_wait_seconds: 30, max_waiting_requests: 0, max_total_attempts: 10, member_stream_idle_timeout_seconds: 60, member_max_attempts: 1, member_retry_interval_seconds: 1, member_non_stream_response_timeout_seconds: 120, member_stream_first_event_timeout_seconds: 30, member_cooldown_seconds: 60, member_affinity_seconds: 0, price_metric: 'blended', metric_window_size: 20, score_price_weight: 0, score_latency_weight: 0, score_success_weight: 0, reliability_floor: 0 },
    random: { request_timeout_seconds: 600, max_wait_seconds: 30, max_waiting_requests: 0, max_total_attempts: 10, member_stream_idle_timeout_seconds: 60, member_max_attempts: 1, member_retry_interval_seconds: 1, member_non_stream_response_timeout_seconds: 120, member_stream_first_event_timeout_seconds: 30, member_cooldown_seconds: 30, member_affinity_seconds: 0, price_metric: 'blended', metric_window_size: 20, score_price_weight: 0, score_latency_weight: 0, score_success_weight: 0, reliability_floor: 0 },
};

// modeRelayConfig 返回该模式的预设; 灰度恒从关闭态起步 —— 它叠加在模式之上, 不属于任何模式的预设。
function modeRelayConfig(mode: GroupMode): GroupRelayConfig {
    return { ...modeRelayDefaults[mode], ...canaryDefaults(), auto_ramp_percent: 5, auto_fallback_failures: 3, auto_fallback_seconds: 60, auto_sticky_seconds: 300, auto_fallback_no_candidate: true, auto_explore_percent: 5, auto_min_task_score: 0, auto_min_quality_score: 0, auto_min_success_rate: 0, auto_pareto_enabled: false, auto_quality_weight: 40, auto_price_weight: 20, auto_latency_weight: 20, auto_success_weight: 20 };
}

function applyModeDefaults(mode: GroupMode, current: GroupRelayConfig): GroupRelayConfig {
    return {
        ...preserveCanary(modeRelayConfig(mode), current),
        standby_grant_id: current.standby_grant_id,
        standby_after_failures: current.standby_after_failures,
    };
}

const memberSelectTrigger = 'w-full min-w-0 whitespace-normal rounded-xl text-left data-[size=default]:h-auto *:data-[slot=select-value]:line-clamp-none *:data-[slot=select-value]:min-w-0 *:data-[slot=select-value]:wrap-anywhere';
const memberSelectContent = 'w-(--radix-select-trigger-width) min-w-0 overflow-hidden [&>[data-radix-select-viewport]]:h-auto';

// PROTOCOL_TAGS 是凭据行上的协议标识。
// 此处写全称: 凭据行只有名称一列, 横向有余量; 渠道表单的授权矩阵是三列复选框, 列宽紧张才用缩写。
const PROTOCOL_TAGS = [
    { bit: Protocol.OpenAIChatCompletion, label: 'Chat' },
    { bit: Protocol.OpenAIResponse, label: 'Response' },
    { bit: Protocol.AnthropicMessage, label: 'Message' },
    { bit: Protocol.XunfeiSpark, label: 'Spark' },
];

// FieldHelp 渲染配置字段的简短帮助提示。
function FieldHelp({ text }: { text: string }) {
    return (
        <Tooltip>
            <TooltipTrigger asChild>
                <button type="button" aria-label={text} className="size-4 shrink-0 cursor-help text-muted-foreground">
                    <HelpCircle className="size-4" />
                </button>
            </TooltipTrigger>
            <TooltipContent side="top" sideOffset={10} align="center" className="max-w-[min(22rem,calc(100vw-2rem))] wrap-anywhere text-pretty">
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
    const modelMeta = useTranslations('model.card');
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
        context_length: grant.context_length,
        max_output: grant.max_output,
        user_price: grant.user_price,
    })), [grantCandidates]);

    const [groupName, setGroupName] = useState(initial?.name ?? '');
    const [mode, setMode] = useState<GroupMode>(initial?.mode ?? 'manual');
    const [relayConfig, setRelayConfig] = useState<GroupRelayConfig>(() => ({
        ...modeRelayConfig(initial?.mode ?? 'manual'),
        ...initial?.relay_config,
        // 灰度四项单独兜一层: 编辑器始终拿到完整的类型, 不必在每个读取处再判空。
        ...canaryOf(initial?.relay_config),
    }));
    // 用户 ID 在输入框里是自由文本(允许中途敲出逗号、负数), 解析放在渲染与提交两处,
    // 中间态只存文本, 免得每敲一个字符就被规整成列表而看不见自己输错了什么。
    const [canaryUserIDsText, setCanaryUserIDsText] = useState(() => formatCanaryUserIDs(canaryOf(initial?.relay_config).canary_user_ids));
    const canaryUserIDsInput = parseCanaryUserIDs(canaryUserIDsText);
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

    // 灰度用户 ID 有非法项时挡住提交: 静默丢掉会让人以为灰度已经生效, 而实际只对了一半用户生效。
    // 手动模式下这一块不存在, 也就不该被它的输入挡住保存。
    const canaryInvalid = canarySubmitBlocked(mode, canaryUserIDsText);
    const isValid = groupKey.length > 0 && selectedMembers.length > 0 && !canaryInvalid;

    const standbyGrantID = selectedMembers.length >= 2 &&
        selectedMembers.some((member) => member.channel_grant_id === relayConfig.standby_grant_id)
        ? relayConfig.standby_grant_id ?? 0
        : 0;
    // 灰度成员只能从已落库的成员里选: 新建分组尚未保存时成员还没有 item_id, 灰度无处可指,
    // 后端创建时也会清掉 canary_item_id, 两边口径一致。
    const canaryMembers = useMemo(
        () => selectedMembers.filter((member) => (member.item_id ?? 0) > 0),
        [selectedMembers],
    );
    // 保存的灰度成员可能已被移出分组, 找不到就回落到「关闭」, 不给一个指向不存在成员的值。
    const canaryItemID = canaryMembers.some((member) => member.item_id === relayConfig.canary_item_id && member.channel_grant_id !== standbyGrantID)
        ? relayConfig.canary_item_id
        : 0;

    const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        if (!isValid) return;
        onSubmit({
            name: groupName,
            mode,
            // 用户列表以输入框为准: 它与 canary_user_ids 是同一份数据, 提交时统一按下标解析结果落库。
            relay_config: canarySubmitConfig({
                ...relayConfig,
                standby_grant_id: standbyGrantID,
                standby_after_failures: standbyGrantID ? relayConfig.standby_after_failures ?? 2 : 0,
                canary_item_id: canaryItemID,
            }, canaryUserIDsText),
            members: selectedMembers,
        });
    };


    return (
        <form onSubmit={handleSubmit} className="flex flex-col h-full min-h-0 ">
            <div className="min-h-0 min-w-0 flex-1 overflow-hidden px-1 has-[.group-relay-panel[data-state=active]]:overflow-y-auto has-[.group-relay-panel[data-state=active]]:overscroll-contain">
                <FieldGroup className="flex h-full min-h-0 min-w-0 flex-col gap-3 has-[.group-relay-panel[data-state=active]]:h-auto">
                    <div className="grid shrink-0 grid-cols-2 gap-2 @min-[36rem]/field-group:gap-4">
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
                                    // 灰度不属于任何模式的预设, 换模式(含切到手动)都原样保留已填的灰度值。
                                    if (!initial) setRelayConfig((prev) => applyModeDefaults(nextMode, prev));
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

                    <Tabs defaultValue="members" className="flex min-h-0 min-w-0 flex-1">
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

                        <TabsContent value="relay" className="group-relay-panel min-w-0 wrap-anywhere px-1 pb-1 [&_[data-slot=field]]:min-w-0">
                            <div className="mb-3 flex flex-wrap items-center justify-between gap-2 rounded-xl border border-border/50 bg-muted/30 px-3 py-2">
                                <span className="text-xs text-muted-foreground">{t('form.presetHint')}</span>
                                <Button type="button" variant="outline" size="sm" className="rounded-lg" onClick={() => setRelayConfig((prev) => applyModeDefaults(mode, prev))}>{t('form.applyPreset')}</Button>
                            </div>
                            {mode === 'score' && (
                                <p className="mb-3 text-sm text-muted-foreground">{t('form.scoreRetryHint')}</p>
                            )}
                            <div className="grid grid-cols-1 gap-3 @min-[36rem]/field-group:grid-cols-2">
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
                                        disabled={mode === 'score'}
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
                                        disabled={mode === 'score'}
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
                                    <FieldLabel htmlFor="group-auto-routing">{t('form.autoRouting')}</FieldLabel>
                                    <Select value={relayConfig.auto_routing ?? 'off'} onValueChange={(value) =>
                                        setRelayConfig((prev) => ({ ...prev, auto_routing: value === 'off' ? undefined : value as 'shadow' | 'active' }))}>
                                        <SelectTrigger id="group-auto-routing" className="w-full rounded-xl"><SelectValue /></SelectTrigger>
                                        <SelectContent>
                                            <SelectItem value="off">{t('form.autoOff')}</SelectItem>
                                            <SelectItem value="shadow">{t('form.autoShadow')}</SelectItem>
                                            <SelectItem value="active">{t('form.autoActive')}</SelectItem>
                                        </SelectContent>
                                    </Select>
                                </Field>
                                {!!relayConfig.auto_routing && (
                                    <div className="col-span-full min-w-0 space-y-3">
                                        <p className="text-sm text-muted-foreground">{t('form.autoHint')}</p>
                                        <p className="text-xs leading-relaxed text-muted-foreground">{t('form.autoRefreshHint')}</p>
                                        {relayConfig.auto_routing === 'active' && (
                                            <>
                                            <div className="grid gap-3 rounded-xl border border-border/50 p-3 @min-[42rem]/field-group:grid-cols-3 @min-[30rem]/field-group:grid-cols-2">
                                                <Field>
                                                    <FieldLabel htmlFor="group-auto-ramp">
                                                        {t('form.autoRampPercent')}
                                                        <FieldHelp text={t('form.autoRampPercentHint')} />
                                                    </FieldLabel>
                                                    <Input
                                                        id="group-auto-ramp"
                                                        type="number"
                                                        min={1}
                                                        max={100}
                                                        step={1}
                                                        value={String(relayConfig.auto_ramp_percent ?? 5)}
                                                        onChange={(event) => {
                                                            const value = Number.parseInt(event.target.value, 10);
                                                            setRelayConfig((prev) => ({ ...prev, auto_ramp_percent: Number.isFinite(value) ? Math.min(Math.max(value, 1), 100) : 5 }));
                                                        }}
                                                        className="rounded-xl"
                                                    />
                                                </Field>
                                                <Field>
                                                    <FieldLabel htmlFor="group-auto-fallback-failures">
                                                        {t('form.autoFallbackFailures')}
                                                        <FieldHelp text={t('form.autoFallbackFailuresHint')} />
                                                    </FieldLabel>
                                                    <Input
                                                        id="group-auto-fallback-failures"
                                                        type="number"
                                                        min={1}
                                                        max={100}
                                                        step={1}
                                                        value={String(relayConfig.auto_fallback_failures ?? 3)}
                                                        onChange={(event) => {
                                                            const value = Number.parseInt(event.target.value, 10);
                                                            setRelayConfig((prev) => ({ ...prev, auto_fallback_failures: Number.isFinite(value) ? Math.min(Math.max(value, 1), 100) : 3 }));
                                                        }}
                                                        className="rounded-xl"
                                                    />
                                                </Field>
                                                <Field>
                                                    <FieldLabel htmlFor="group-auto-fallback-seconds">
                                                        {t('form.autoFallbackSeconds')}
                                                        <FieldHelp text={t('form.autoFallbackSecondsHint')} />
                                                    </FieldLabel>
                                                    <Input
                                                        id="group-auto-fallback-seconds"
                                                        type="number"
                                                        min={1}
                                                        max={3600}
                                                        step={1}
                                                        value={String(relayConfig.auto_fallback_seconds ?? 60)}
                                                        onChange={(event) => {
                                                            const value = Number.parseInt(event.target.value, 10);
                                                            setRelayConfig((prev) => ({ ...prev, auto_fallback_seconds: Number.isFinite(value) ? Math.min(Math.max(value, 1), 3600) : 60 }));
                                                        }}
                                                        className="rounded-xl"
                                                    />
                                                </Field>
                                                <Field>
                                                    <FieldLabel htmlFor="group-auto-sticky-seconds">
                                                        {t('form.autoStickySeconds')}
                                                        <FieldHelp text={t('form.autoStickySecondsHint')} />
                                                    </FieldLabel>
                                                    <Input
                                                        id="group-auto-sticky-seconds"
                                                        type="number"
                                                        min={0}
                                                        max={3600}
                                                        step={1}
                                                        value={String(relayConfig.auto_sticky_seconds ?? 0)}
                                                        onChange={(event) => {
                                                            const value = Number.parseInt(event.target.value, 10);
                                                            setRelayConfig((prev) => ({ ...prev, auto_sticky_seconds: Number.isFinite(value) ? Math.min(Math.max(value, 0), 3600) : 0 }));
                                                        }}
                                                        className="rounded-xl"
                                                    />
                                                </Field>
                                            </div>
                                            <div className="rounded-xl border border-border/50 p-3">
                                                <h4 className="mb-1 text-sm font-medium">{t('form.autoQualityTitle')}</h4>
                                                <p className="mb-3 text-xs leading-relaxed text-muted-foreground">{t('form.autoQualityHint')}</p>
                                                <div className="grid gap-3 @min-[42rem]/field-group:grid-cols-3 @min-[30rem]/field-group:grid-cols-2">
                                                    <Field>
                                                        <FieldLabel htmlFor="group-auto-min-score">{t('form.autoMinTaskScore')}</FieldLabel>
                                                        <Input id="group-auto-min-score" type="number" min={0} max={100} step={1}
                                                            value={String(relayConfig.auto_min_task_score ?? 0)}
                                                            onChange={(event) => setRelayConfig((prev) => ({ ...prev, auto_min_task_score: Math.min(Math.max(Number.parseInt(event.target.value, 10) || 0, 0), 100) }))}
                                                            className="rounded-xl" />
                                                    </Field>
                                                    <Field>
                                                        <FieldLabel htmlFor="group-auto-min-quality-score">{t('form.autoMinQualityScore')}</FieldLabel>
                                                        <Input id="group-auto-min-quality-score" type="number" min={0} max={100} step={1}
                                                            value={String(relayConfig.auto_min_quality_score ?? 0)}
                                                            onChange={(event) => setRelayConfig((prev) => ({ ...prev, auto_min_quality_score: Math.min(Math.max(Number.parseInt(event.target.value, 10) || 0, 0), 100) }))}
                                                            className="rounded-xl" />
                                                    </Field>
                                                    <Field>
                                                        <FieldLabel htmlFor="group-auto-min-success-rate">{t('form.autoMinSuccessRate')}</FieldLabel>
                                                        <Input id="group-auto-min-success-rate" type="number" min={0} max={100} step={1}
                                                            value={String(relayConfig.auto_min_success_rate ?? 0)}
                                                            onChange={(event) => setRelayConfig((prev) => ({ ...prev, auto_min_success_rate: Math.min(Math.max(Number.parseInt(event.target.value, 10) || 0, 0), 100) }))}
                                                            className="rounded-xl" />
                                                    </Field>
                                                    {([
                                                        ['auto_quality_weight', 'autoQualityWeight'],
                                                        ['auto_price_weight', 'autoPriceWeight'],
                                                        ['auto_latency_weight', 'autoLatencyWeight'],
                                                        ['auto_success_weight', 'autoSuccessWeight'],
                                                    ] as const).map(([key, label]) => (
                                                        <Field key={key}>
                                                            <FieldLabel htmlFor={`group-${key}`}>{t(`form.${label}`)}</FieldLabel>
                                                            <Input id={`group-${key}`} type="number" min={0} max={100} step={1}
                                                                value={String(relayConfig[key] ?? 0)}
                                                                onChange={(event) => setRelayConfig((prev) => ({ ...prev, [key]: Math.min(Math.max(Number.parseInt(event.target.value, 10) || 0, 0), 100) }))}
                                                                className="rounded-xl" />
                                                        </Field>
                                                    ))}
                                                </div>
                                                <p className="mt-2 text-[11px] leading-relaxed text-muted-foreground">
                                                    {t('form.autoQualityRegistryHint')}
                                                </p>
                                                <label className="mt-3 flex min-w-0 items-start gap-2 rounded-xl border border-border/50 p-3 text-xs">
                                                    <Checkbox
                                                        checked={!!relayConfig.auto_pareto_enabled}
                                                        onCheckedChange={(value) => setRelayConfig((prev) => ({ ...prev, auto_pareto_enabled: value === true }))}
                                                        className="mt-0.5"
                                                        aria-label={t('form.autoParetoEnabled')}
                                                    />
                                                    <span>
                                                        <span className="block font-medium">{t('form.autoParetoEnabled')}</span>
                                                        <span className="mt-1 block leading-relaxed text-muted-foreground">{t('form.autoParetoEnabledHint')}</span>
                                                    </span>
                                                </label>
                                                <div className="mt-3 grid gap-3 @min-[36rem]/field-group:grid-cols-2">
                                                    <label className="flex min-w-0 items-start gap-2 rounded-xl border border-border/50 p-3 text-xs">
                                                        <Checkbox
                                                            checked={!!relayConfig.auto_fallback_no_candidate}
                                                            onCheckedChange={(value) => setRelayConfig((prev) => ({ ...prev, auto_fallback_no_candidate: value === true }))}
                                                        />
                                                        <span>
                                                            <span className="block font-medium">{t('form.autoFallbackNoCandidate')}</span>
                                                            <span className="mt-1 block leading-relaxed text-muted-foreground">{t('form.autoFallbackNoCandidateHint')}</span>
                                                        </span>
                                                    </label>
                                                    <Field>
                                                        <FieldLabel htmlFor="group-auto-explore-percent">
                                                            {t('form.autoExplorePercent')}
                                                            <FieldHelp text={t('form.autoExplorePercentHint')} />
                                                        </FieldLabel>
                                                        <Input id="group-auto-explore-percent" type="number" min={0} max={20} step={1}
                                                            value={String(relayConfig.auto_explore_percent ?? 0)}
                                                            onChange={(event) => setRelayConfig((prev) => ({ ...prev, auto_explore_percent: Math.min(Math.max(Number.parseInt(event.target.value, 10) || 0, 0), 20) }))}
                                                            className="rounded-xl" />
                                                    </Field>
                                                </div>
                                            </div>
                                            </>
                                        )}
                                        <div className="grid items-start gap-3 @min-[36rem]/field-group:grid-cols-2">
                                            {([
                                                ['auto_general', 'autoGeneral'],
                                                ['auto_summary', 'autoSummary'],
                                                ['auto_coding', 'autoCoding'],
                                                ['auto_tool', 'autoTool'],
                                                ['auto_extract', 'autoExtract'],
                                            ] as const).map(([key, label]) => {
                                                const selected = parseAutoGrantIDs(relayConfig[key]);
                                                return (
                                                    <Field key={key} className={key === 'auto_tool' ? 'col-span-full' : undefined}>
                                                        <FieldLabel>{t(`form.${label}`)}</FieldLabel>
                                                        <Input
                                                            type="number"
                                                            min={0}
                                                            max={100}
                                                            aria-label={t('form.autoTaskRamp', { task: t(`form.${label}`) })}
                                                            placeholder={t('form.autoTaskRampDefault', { percent: relayConfig.auto_ramp_percent || 100 })}
                                                            value={relayConfig.auto_task_ramp_percent?.[key === 'auto_extract' ? 'extraction' : key.slice(5)] ?? ''}
                                                            onChange={(event) => {
                                                                const value = event.target.value;
                                                                const task = key === 'auto_extract' ? 'extraction' : key.slice(5);
                                                                setRelayConfig((previous) => {
                                                                    const ramps = { ...previous.auto_task_ramp_percent };
                                                                    if (value === '') delete ramps[task];
                                                                    else ramps[task] = Math.min(100, Math.max(0, Math.trunc(Number(value))));
                                                                    return { ...previous, auto_task_ramp_percent: ramps };
                                                                });
                                                            }}
                                                        />
                                                        {selectedMembers.length === 0 ? (
                                                            <p className="text-xs text-muted-foreground">{t('form.autoNoMembers')}</p>
                                                        ) : (
                                                            <div className="grid gap-1 rounded-xl border border-border/50 p-2">
                                                                {selectedMembers.map((member) => {
                                                                    const checked = selected.includes(member.channel_grant_id);
                                                                    const inputID = `${key}-${member.channel_grant_id}`;
                                                                    return (
                                                                        <div key={member.channel_grant_id} className="min-w-0 rounded-lg px-2 py-1.5 text-xs hover:bg-muted/60">
                                                                            <label htmlFor={inputID} className="flex cursor-pointer items-start gap-2">
                                                                                <Checkbox
                                                                                    id={inputID}
                                                                                    checked={checked}
                                                                                    onCheckedChange={(value) => setRelayConfig((prev) => ({
                                                                                        ...prev,
                                                                                        [key]: autoGrantIDsValue(
                                                                                            selectedMembers,
                                                                                            toggleAutoGrantID(parseAutoGrantIDs(prev[key]), member.channel_grant_id, value === true),
                                                                                        ),
                                                                                    } satisfies Partial<Record<AutoTaskConfigField, string>>))}
                                                                                />
                                                                                <span className="min-w-0 flex-1 space-y-0.5">
                                                                                    <span className="block font-medium">{member.name}</span>
                                                                                    <span className="block text-muted-foreground">{member.channel_name} · {member.key_name}</span>
                                                                                    {!!(member.context_length || member.max_output) && (
                                                                                        <span className="flex flex-wrap gap-x-3 text-muted-foreground">
                                                                                            <span>{modelMeta('context')} {member.context_length || '?'}</span>
                                                                                            <span>{modelMeta('maxOutput')} {member.max_output || '?'}</span>
                                                                                        </span>
                                                                                    )}
                                                                                </span>
                                                                            </label>
                                                                            {key === 'auto_tool' && checked && !!(member.protocols & Protocol.OpenAIChatCompletion) && (
                                                                                <ToolProbeControls grantID={member.channel_grant_id} score={relayConfig.auto_min_task_score || 80} />
                                                                            )}
                                                                            {key === 'auto_tool' && checked && !!(member.protocols & Protocol.AnthropicMessage) && (
                                                                                <ToolProbeControls grantID={member.channel_grant_id} score={0} onAnthropicVerified={() => setRelayConfig((previous) => ({
                                                                                    ...previous,
                                                                                    auto_anthropic_tool_verified: autoGrantIDsValue(selectedMembers, toggleAutoGrantID(parseAutoGrantIDs(previous.auto_anthropic_tool_verified), member.channel_grant_id, true)),
                                                                                }))} />
                                                                            )}
                                                                            {key === 'auto_tool' && checked && !!(member.protocols & Protocol.AnthropicMessage) && (
                                                                                <ToolProbeControls grantID={member.channel_grant_id} score={0} stream onAnthropicVerified={() => setRelayConfig((previous) => ({
                                                                                    ...previous,
                                                                                    auto_anthropic_stream_verified: autoGrantIDsValue(selectedMembers, toggleAutoGrantID(parseAutoGrantIDs(previous.auto_anthropic_stream_verified), member.channel_grant_id, true)),
                                                                                }))} />
                                                                            )}
                                                                            {key === 'auto_tool' && checked && (
                                                                                <div className="mt-2 grid gap-2 text-muted-foreground @min-[36rem]/field-group:grid-cols-2">
                                                                                    {([
                                                                                        ['auto_tool_verified', 'autoToolVerified'],
                                                                                        ['auto_responses_tool_verified', 'autoResponsesToolVerified'],
                                                                                        ['auto_anthropic_tool_verified', 'autoAnthropicToolVerified'],
                                                                                        ['auto_anthropic_stream_verified', 'autoAnthropicStreamVerified'],
                                                                                    ] as const).map(([field, label]) => {
                                                                                        if (field === 'auto_anthropic_stream_verified' && !(member.protocols & Protocol.AnthropicMessage)) return null;
                                                                                        return (
                                                                                            <label key={field} className="flex min-w-0 cursor-pointer items-start gap-2">
                                                                                                <Checkbox
                                                                                                    checked={parseAutoGrantIDs(relayConfig[field]).includes(member.channel_grant_id)}
                                                                                                    onCheckedChange={(value) => setRelayConfig((previous) => ({
                                                                                                        ...previous,
                                                                                                        [field]: autoGrantIDsValue(selectedMembers, toggleAutoGrantID(parseAutoGrantIDs(previous[field]), member.channel_grant_id, value === true)),
                                                                                                    }))}
                                                                                                />
                                                                                                <span className="min-w-0">{t(`form.${label}`)}</span>
                                                                                            </label>
                                                                                        );
                                                                                    })}
                                                                                </div>
                                                                            )}
                                                                        </div>
                                                                    );
                                                                })}
                                                            </div>
                                                        )}
                                                    </Field>
                                                );
                                            })}
                                        </div>
                                        <label className="flex items-start gap-2 text-xs">
                                            <Checkbox
                                                checked={relayConfig.auto_require_anthropic_stream_probe ?? false}
                                                onCheckedChange={(value) => setRelayConfig((previous) => ({
                                                    ...previous, auto_require_anthropic_stream_probe: value === true,
                                                }))}
                                            />
                                            <span>
                                                {t('form.requireAnthropicStreamProbe')}
                                                <span className="mt-1 block text-muted-foreground">{t('form.requireAnthropicStreamProbeHint')}</span>
                                            </span>
                                        </label>
                                        {selectedMembers.length > 0 && (
                                            <div className="space-y-2 rounded-xl border border-border/50 p-3">
                                                <div>
                                                    <span className="text-sm font-medium">{t('form.autoContextOverrides')}</span>
                                                    <p className="text-xs text-muted-foreground">{t('form.autoContextOverridesHint')}</p>
                                                </div>
                                                <div className="grid gap-2 @min-[36rem]/field-group:grid-cols-2">
                                                    {selectedMembers.map((member) => {
                                                        const overrides = parseContextOverrides(relayConfig.auto_context_overrides);
                                                        return (
                                                            <label key={`context-${member.channel_grant_id}`} className="flex min-w-0 items-start gap-2 text-xs text-muted-foreground">
                                                                <span className="min-w-0 flex-1">{member.channel_name} · {member.name}</span>
                                                                <Input
                                                                    type="number"
                                                                    min={1}
                                                                    step={1}
                                                                    placeholder={member.context_length ? String(member.context_length) : t('form.autoContextUnknown')}
                                                                    value={overrides[member.channel_grant_id] ? String(overrides[member.channel_grant_id]) : ''}
                                                                    onChange={(event) => {
                                                                        const next = { ...overrides };
                                                                        const value = Number.parseInt(event.target.value, 10);
                                                                        if (Number.isFinite(value) && value > 0) next[member.channel_grant_id] = value;
                                                                        else delete next[member.channel_grant_id];
                                                                        setRelayConfig((prev) => ({ ...prev, auto_context_overrides: contextOverridesValue(next) }));
                                                                    }}
                                                                    className="h-8 w-28 shrink-0 rounded-lg"
                                                                    aria-label={t('form.autoContextOverrideFor', { model: member.name })}
                                                                />
                                                            </label>
                                                        );
                                                    })}
                                                </div>
                                            </div>
                                        )}
                                    </div>
                                )}
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
                            {mode !== 'manual' && (
                                <section className="mt-4 space-y-3 rounded-xl border border-border/50 bg-muted/20 p-3">
                                    <div>
                                        <h4 className="text-sm font-semibold">{t('form.standbyTitle')}</h4>
                                        <p className="mt-1 text-xs leading-relaxed text-muted-foreground">{t('form.standbyHint')}</p>
                                    </div>
                                    {selectedMembers.length < 2 ? (
                                        <p className="text-xs text-muted-foreground">{t('form.standbyNeedsMembers')}</p>
                                    ) : (
                                        <Field>
                                            <FieldLabel htmlFor="group-standby-member">{t('form.standbyMember')}</FieldLabel>
                                            <Select
                                                value={String(standbyGrantID)}
                                                onValueChange={(value) => setRelayConfig((prev) => ({ ...prev, standby_grant_id: Number(value) }))}
                                            >
                                                <SelectTrigger id="group-standby-member" className={memberSelectTrigger}><SelectValue /></SelectTrigger>
                                                <SelectContent className={memberSelectContent} align="start" side="top" collisionPadding={8}>
                                                    <SelectItem value="0">{t('form.canaryOff')}</SelectItem>
                                                    {selectedMembers.map((member) => (
                                                        <SelectItem key={member.channel_grant_id} value={String(member.channel_grant_id)} className="min-w-0 wrap-anywhere">
                                                            {member.channel_name} · {member.name} · {member.key_name}
                                                        </SelectItem>
                                                    ))}
                                                </SelectContent>
                                            </Select>
                                        </Field>
                                    )}
                                    {standbyGrantID > 0 && (
                                        <Field className="max-w-xs">
                                            <FieldLabel htmlFor="group-standby-failures">{t('form.standbyFailures')}</FieldLabel>
                                            <Input id="group-standby-failures" type="number" min={1} max={10} step={1}
                                                value={relayConfig.standby_after_failures || 2}
                                                onChange={(event) => setRelayConfig((prev) => ({
                                                    ...prev,
                                                    standby_after_failures: Math.min(10, Math.max(1, Math.trunc(Number(event.target.value)))),
                                                }))}
                                                className="rounded-xl"
                                            />
                                        </Field>
                                    )}
                                </section>
                            )}
                            {/* 灰度分流叠加在上面的模式之上, 手动模式除外 —— 手动的成员是硬约束, 没有可叠加的余地,
                                故整块不渲染而不是渲染成禁用态。 */}
                            {mode !== 'manual' && (
                                <section className="mt-4 rounded-xl border border-border/50 bg-muted/20 p-3">
                                    <h4 className="mb-1 text-sm font-semibold">{t('form.canaryTitle')}</h4>
                                    <p className="mb-3 text-xs leading-relaxed text-muted-foreground">{t('form.canaryHint')}</p>
                                    <div className="grid grid-cols-1 gap-3 @min-[36rem]/field-group:grid-cols-2">
                                        <Field className="col-span-full">
                                            <FieldLabel htmlFor="group-canary-member">
                                                {t('form.canaryMember')}
                                                <FieldHelp text={t('form.canaryMemberHint')} />
                                            </FieldLabel>
                                            {canaryMembers.length === 0 ? (
                                                // 新建分组尚未保存时成员还没有主键, 灰度无处可指; 给出原因而不是一个空下拉。
                                                <p className="text-xs text-muted-foreground">{t('form.canaryMemberEmpty')}</p>
                                            ) : (
                                                <Select
                                                    value={String(canaryItemID)}
                                                    onValueChange={(value) => setRelayConfig((prev) => ({ ...prev, canary_item_id: Number(value) }))}
                                                >
                                                    <SelectTrigger id="group-canary-member" className={memberSelectTrigger}>
                                                        <SelectValue />
                                                    </SelectTrigger>
                                                    <SelectContent
                                                        className={memberSelectContent}
                                                        align="start"
                                                        side="top"
                                                        collisionPadding={8}
                                                    >
                                                        {/* 「关闭」恒为首项: 灰度是可选叠加, 必须能一键回到关闭。 */}
                                                        <SelectItem value="0">{t('form.canaryOff')}</SelectItem>
                                                        {canaryMembers.filter((member) => member.channel_grant_id !== standbyGrantID).map((member) => (
                                                            <SelectItem key={member.item_id} value={String(member.item_id)} className="min-w-0 wrap-anywhere">
                                                                {member.channel_name} · {member.name}
                                                            </SelectItem>
                                                        ))}
                                                    </SelectContent>
                                                </Select>
                                            )}
                                        </Field>
                                        <Field>
                                            <FieldLabel htmlFor="group-canary-percent">
                                                {t('form.canaryPercent')}
                                                <FieldHelp text={t('form.canaryPercentHint')} />
                                            </FieldLabel>
                                            <div className="flex items-center gap-2">
                                                <Input
                                                    id="group-canary-percent"
                                                    type="number"
                                                    inputMode="numeric"
                                                    min={0}
                                                    max={100}
                                                    step={1}
                                                    value={String(relayConfig.canary_percent)}
                                                    onChange={(event) => {
                                                        const value = Number.parseInt(event.target.value, 10);
                                                        setRelayConfig((prev) => ({ ...prev, canary_percent: Number.isFinite(value) ? Math.min(Math.max(value, 0), 100) : 0 }));
                                                    }}
                                                    className="w-20 rounded-xl"
                                                />
                                                {/* 滑块与数字框同源: 粗调靠拖, 精确值靠输入。 */}
                                                <input
                                                    type="range"
                                                    min={0}
                                                    max={100}
                                                    step={1}
                                                    value={relayConfig.canary_percent}
                                                    aria-label={t('form.canaryPercent')}
                                                    onChange={(event) => {
                                                        const value = Number.parseInt(event.target.value, 10);
                                                        setRelayConfig((prev) => ({ ...prev, canary_percent: Number.isFinite(value) ? Math.min(Math.max(value, 0), 100) : 0 }));
                                                    }}
                                                    className="h-1.5 min-w-0 flex-1 accent-primary"
                                                />
                                            </div>
                                        </Field>
                                        <Field>
                                            <FieldLabel htmlFor="group-canary-users">
                                                {t('form.canaryUserIDs')}
                                                <FieldHelp text={t('form.canaryUserIDsHint')} />
                                            </FieldLabel>
                                            <Input
                                                id="group-canary-users"
                                                value={canaryUserIDsText}
                                                placeholder={t('form.canaryUserIDsPlaceholder')}
                                                aria-invalid={canaryUserIDsInput.invalid.length > 0}
                                                onChange={(event) => setCanaryUserIDsText(event.target.value)}
                                                className="rounded-xl"
                                            />
                                            {canaryUserIDsInput.invalid.length > 0 && (
                                                <p role="alert" className="text-xs text-destructive">
                                                    {t('form.canaryUserIDsInvalid', { values: canaryUserIDsInput.invalid.join(', ') })}
                                                </p>
                                            )}
                                        </Field>
                                        <Field>
                                            <FieldLabel htmlFor="group-canary-per-request">
                                                {t('form.canaryPerRequest')}
                                                <FieldHelp text={t('form.canaryPerRequestHint')} />
                                            </FieldLabel>
                                            <div className="flex items-center gap-2">
                                                <Switch
                                                    id="group-canary-per-request"
                                                    checked={relayConfig.canary_per_request}
                                                    onCheckedChange={(checked) => setRelayConfig((prev) => ({ ...prev, canary_per_request: checked }))}
                                                />
                                                <span className="text-xs text-muted-foreground">{t('form.canaryPerRequestHint')}</span>
                                            </div>
                                        </Field>
                                    </div>
                                </section>
                            )}
                        </TabsContent>
                    </Tabs>
                </FieldGroup>
            </div>

            <div className="mt-auto shrink-0 pt-2 sm:pt-3">
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
