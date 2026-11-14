import { queryOptions, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';
import type { LLMPrice } from './share';
import { channelStatsQueryOptions, groupListQueryOptions, modelListQueryOptions } from './queries';
import { formatStatsMetrics, type StatsMetrics, type StatsMetricsFormatted } from './stats';
import { POLL_MINUTE, POLL_SLOW } from '@/lib/polling';

// Protocol 是渠道支持的上游线协议位掩码，位值与后端 model.Protocol 一致，不可变更。
// 一条授权可同时支持多个协议，按位或组合；1 << 0 由后端保留待用。
export const Protocol = {
    OpenAIChatCompletion: 1 << 1,
    OpenAIResponse: 1 << 2,
    AnthropicMessage: 1 << 3,
    OpenAIImage: 1 << 4, // 生图协议(generations / edits); P2 视频为 1 << 5。
    XunfeiSpark: 1 << 6, // 讯飞星火协议。1 << 0 保留、1 << 5 已被视频占坑, 新位只能顺延; 落库后与后端 model.Protocol 一致, 同样不可再变更。
} as const;

// Dialect 是上游在标准协议之上的方言，决定出站转换器的厂商特化配置。
// 地址与路径不属于方言范畴，由前端按服务商预填到渠道字段上。
export type Dialect = 'generic' | 'xunfei';

type CustomHeader = {
    header_key: string;
    header_value: string;
};

// ChannelKey 是渠道下的一份上游凭据；名称在渠道内唯一，读写都按它引用。
export type ChannelKey = {
    name: string;
    key: string; // 上游访问凭据; 讯飞渠道用它承载 API Key; 导入的账号凭据则承载整包账号 JSON。
    // 讯飞星火专用字段(dialect=xunfei 才填写); 读侧可能缺省, 写侧恒带三项。
    app_id?: string;
    api_secret?: string;
    domain?: string;
    enabled: boolean;
    // 导入的上游账号(见 model/account_json.go)。非账号凭据这些字段全空,
    // 因此界面据 account_kind 决定"显示成普通 Key 还是显示成账号"。
    account_kind?: '' | 'oauth' | 'cookie';
    account_platform?: '' | 'codex' | 'kiro' | 'gemini' | 'grok' | 'claude';
    account_email?: string;
    // token_expires_at 是当前访问令牌的到期时间; cookie 类账号没有这个字段(它们无法续期)。
    token_expires_at?: string;
    // token_refresh_at 是最近一次刷新成功的时间; 空表示从未刷新过。
    token_refresh_at?: string;
    // disabled_until 是凭据级熔断的截止时间(后端 ChannelKeyConfig 一直带着它)。
    // 账号界面靠它区分"到期时间还在未来但其实转发不出去"的那种状态。
    disabled_until?: string | null;
    // breaker_trips 是自上次成功以来累计熔断次数; 连续失败会翻倍熔断时长。
    breaker_trips?: number;
    // error_count 是熔断前的连续错误数(熔断触发时会清零), 不是累计错误总数。
    error_count?: number;
};

// ChannelGrant 是渠道内的一条上游授权：指定模型使用指定凭据时支持的协议集合，也是转发的最小单位。
// 两侧按名称引用，读写同构：名称在渠道内唯一，新增的模型与凭据在后端同一事务内才分配主键，
// 故提交侧只能按名称引用，读侧也随之给名称，页面无需在主键与名称之间翻译。
// 授权本身没有启用状态: 不再授权即删掉该组合, 临时收回则停用凭据或摘掉协议位。
export type ChannelGrant = {
    model_name: string;
    key_name: string;
    protocols: number; // Protocol 位掩码。
};

// ChannelGrantCandidate 是分组页可选取的一条授权，字段与 GroupItem 的展示字段一一对应。
// 分组页由此不必拉整份渠道列表：那里带着统计、路径、代理与凭据明文，与选取成员无关。
// available 与分组成员同一口径，均由后端定稿，前后端不会各判一套。
export type ChannelGrantCandidate = {
    id: number; // 授权主键，分组成员按它引用。
    channel_id: number;
    channel_name: string; // 非自有渠道此值为发布方的唯一编码，不暴露真实渠道名。
    model_name: string;
    key_name: string; // 非自有渠道此值为空，不暴露上游凭据名。
    protocols: number; // Protocol 位掩码。
    available: boolean;
    listed: boolean; // 上架状态；非自有渠道只有上架模型才会出现在候选里。
    supply_price: LLMPrice; // 供货价（渠道商收入口径）。
    user_price: LLMPrice; // 上浮后的用户价；未定价的上架模型为全 0。
};

/**
 * 渠道完整配置（与后端 model.ChannelDetail 对齐）
 * 读写同构：编辑表单读到什么形状就提交什么形状，故创建与更新共用此类型，无需另建提交类型。
 * 提交即全量，未列出的凭据与模型会被删除并级联删除其授权；创建时 id 取 0，由后端分配。
 * keys、models、grants 和 custom_header 恒为数组，后端读取侧承诺不为 null。
 */
// ChannelEgress 是渠道的一条出口线路。
// kind=direct 时 url 恒为空；http/socks5 时 url 必填且协议要与 kind 一致（后端会校验）。
export type ChannelEgress = {
    id: number;
    name: string;
    kind: 'direct' | 'http' | 'socks5';
    url: string;
    region: string; // 出口所在地区码（如 HK），留空表示不参与区域亲和。
    weight: number; // 同分时的优先权重，数值越大越先被选中。
    enabled: boolean;
};

export type ChannelEgressHealth = {
    id: number;
    name: string;
    kind: 'direct' | 'http' | 'socks5';
    region: string;
    weight: number;
    enabled: boolean;
    samples: number;
    has_data: boolean;
    success_rate: number;
    avg_wait_ms: number;
    cooldown_until?: string | null;
    cooldown_remaining_ms: number;
    cooling: boolean;
    updated_at?: string | null;
};

export type ChannelEgressHealthResponse = {
    channel_id: number;
    egresses: ChannelEgressHealth[];
};

export type ChannelDetail = {
    id: number;
    name: string;
    dialect: Dialect;
    enabled: boolean;
    base_url: string; // 上游地址，各协议共用。
    openai_chat_completion_path: string;
    openai_response_path: string;
    anthropic_message_path: string;
    keys: ChannelKey[];
    // egresses 是渠道的出口池：每条出口描述"从哪条线路出去"，转发时按实测耗时择优。
    // 留空表示沿用渠道自身的代理配置（proxy / channel_proxy），保持旧行为。
    egresses: ChannelEgress[];
    models: string[]; // 上游模型名称；模型除名称外没有界面用得上的字段。
    grants: ChannelGrant[];
    proxy: boolean;
    custom_header: CustomHeader[];
    param_override: string;
    channel_proxy: string;
    match_regex: string;
    model_auto_add: boolean; // 保存后自动探测上游模型并入渠道; 默认关, 只增不删、授权按位 OR。
    /** 渠道级上浮比例; -1 = 跟随全局 markup_ratio。渠道商自己的利润决策, 不动供货价原币口径。 */
    markup_ratio?: number;
};

// ChannelModelStats 是单个渠道模型的累计统计，自带名称与熔断状态。
// 熔断字段来自渠道模型自身（不是统计表）：count 与熔断共用同一列，熔断触发时清零，
// 故它表示"熔断前的连续错误数"。disabled_until 有值即表示当前正在熔断。
export type ChannelModelStats = StatsMetrics & {
    model_id: number;
    model_name: string;
    error_count?: number;
    // breaker_trips 是自上次成功以来累计熔断的次数, 熔断时长按它翻倍, 成功即清零。
    breaker_trips?: number;
    last_error_at?: string | null;
    disabled_until?: string | null;
};

// ChannelStats 是单个渠道及其模型的累计统计，自带名称与启停状态。
// 这一份同时充当渠道列表项：列表页要展示的名称、启停与模型个数（即 models.length）都在此，
// 故没有单独的渠道概览接口，整份配置在点开编辑时由 useChannelDetail 单独取。
export type ChannelStats = StatsMetrics & {
    channel_id: number;
    user_id: number; // 渠道归属者，管理员视图下用于区分他人渠道。
    channel_name: string;
    enabled: boolean;
    shared: boolean; // 是否已发布给用户。
    share_code: string; // 发布后对外展示的编码（4 位纯数字，1001 起），未发布为空。
    review_status: ChannelReviewStatus; // 准入审核状态，卡片徽章与发布闸口提示都从这里取。
    review_note: string; // 提交说明 / 审核意见 / 驳回原因。
    submitted_at?: string | null;
    reviewed_at?: string | null;
    trust_level: ChannelTrustLevel; // 信任级别，观察期进度条与质量徽章的数据源。
    observation_start?: string | null;
    observation_until?: string | null;
    // disabled_until 是渠道级的"余额/额度不足暂停"截止时间，与 enabled（人工开关）和
    // 模型级 disabled_until（故障熔断）是三件互不相干的事：人工开关不会被它改写，
    // 单次成功也不会提前解除它——只有到期或人工恢复。
    disabled_until?: string | null;
    models: ChannelModelStats[];
};

// ChannelTrustLevel 是渠道观察期状态机（P2）。
// observing 审核通过后的默认态，允许转发但评分折减；trusted 已转正；
// probation 首轮不达标延长观察；revoked 再次不达标，收回发布资格。
export type ChannelTrustLevel = 'trusted' | 'observing' | 'probation' | 'revoked';

// ChannelReviewStatus 是渠道准入审核状态机的五个态（P1）。
// draft 新建默认；pending 已提交待审；approved 允许发布与进对外候选；
// rejected 附原因待改；stale 通过后关键配置指纹失配待复审——只挡新暴露，存量分组流量不断。
export type ChannelReviewStatus = 'draft' | 'pending' | 'approved' | 'rejected' | 'stale';

// ChannelReviewItem 是审核队列条目，只带审核决策需要的字段，不含路径与凭据。
export type ChannelReviewItem = {
    channel_id: number;
    user_id: number;
    owner_name: string;
    channel_name: string;
    review_status: ChannelReviewStatus;
    review_note: string;
    submitted_at?: string | null;
    reviewed_by: number;
    reviewer_name: string;
    reviewed_at?: string | null;
    shared: boolean;
    enabled: boolean;
    model_count: number;
};

// ChannelModelStatsFormatted 是单个渠道模型的展示用统计。
// disabledUntil 保留原始时间戳而非格式化字符串：界面要按"还剩多久"实时倒计时，需要可比较的时间值。
export type ChannelModelStatsFormatted = {
    model_id: number;
    model_name: string;
    error_count: number;
    breakerTrips: number;
    disabledUntil: string | null;
    lastErrorAt: string | null;
    formatted: StatsMetricsFormatted;
};

// ChannelStatsFormatted 是单个渠道及其模型的展示用统计，同时充当渠道列表项。
// 名称与启停随统计一并给出，列表页由此只消费这一条查询：模型个数即 models.length，
// 整份配置在点开编辑时由 useChannelDetail 单独取。
export type ChannelStatsFormatted = {
    channel_id: number;
    user_id: number;
    channel_name: string;
    enabled: boolean;
    shared: boolean;
    share_code: string;
    review_status: ChannelReviewStatus;
    review_note: string;
    submitted_at: string | null;
    reviewed_at: string | null;
    trust_level: ChannelTrustLevel;
    observation_start: string | null;
    observation_until: string | null;
    // 渠道级额度暂停的截止时间，未暂停为 null；与模型级 disabledUntil 同风格，界面按剩余时长展示。
    disabledUntil: string | null;
    models: ChannelModelStatsFormatted[];
    formatted: StatsMetricsFormatted;
};

// FetchModelRequest 按指定凭据试拉上游模型列表。
// 渠道尚未保存时也可试拉，故随请求携带整份渠道配置：探测用的地址、协议路径、代理、Header 与过滤表达式
// 必须和保存后生效的完全一致，直接给编辑态即可，探测用不上的字段后端忽略。
// 名称可为空：探测常发生在渠道尚未命名时，后端只要求地址非空。
// 不带协议：后端一次同时探 OpenAI 与 Anthropic 两侧，协议支持由各侧响应决定。
type FetchModelRequest = {
    channel: Omit<ChannelDetail, 'id' | 'keys' | 'models' | 'grants' | 'egresses'>;
    key: string;
    // 讯飞星火签名所需凭据；通用渠道保持可选以兼容旧探测接口。
    app_id?: string;
    api_secret?: string;
    domain?: string;
};

// FetchModel 是探测到的单个上游模型及其支持的协议集合。
// OpenAI 侧记为 Responses 而非 Chat：Chat Completions 已被官方标记弃用，需要 Chat 的渠道由用户手动勾选。
export type FetchModel = {
    name: string;
    protocols: number; // Protocol 位掩码。
};

// ChannelQualitySnapshot 是渠道某一天的质量快照（P3）。
// 由后端日任务固化，不可变：前端只读，用于 30 天趋势图与等级徽章。
export type ChannelQualitySnapshot = {
    channel_id: number;
    date: string; // 20060102
    request_count: number;
    success_count: number;
    fail_count: number;
    success_rate: number; // 0..1
    avg_wait_ms: number;
    uptime_pct: number; // 0..100
    health_checks: number;
    health_fails: number;
    grade: '' | 'A' | 'B' | 'C' | 'D'; // 空串表示样本不足不评级
    created_at: string;
};

// ChannelQualitySummary 是渠道当前质量概览与观察期进度。
export type ChannelQualitySummary = {
    channel_id: number;
    trust_level: ChannelTrustLevel;
    review_status: ChannelReviewStatus;
    observing: boolean;
    observation_start?: string | null;
    observation_until?: string | null;
    observation_days: number;
    min_samples: number;
    success_floor: number; // 0..1
    window_samples: number; // 观察期内累计请求数，进度条分子
    window_success_rate: number; // 观察期内成功率，0..1
    yesterday?: ChannelQualitySnapshot;
    trend: ChannelQualitySnapshot[];
};

// useChannelEgressHealth 拉取渠道出口运行时健康快照。
export function useChannelEgressHealth(channelId: number, enabled = true) {
    return useQuery({
        queryKey: ['channels', 'egress-health', channelId],
        queryFn: () => apiRequest<ChannelEgressHealthResponse>(`/api/v1/channel/egress-health/${channelId}`),
        enabled: enabled && channelId > 0,
        refetchInterval: POLL_SLOW,
        refetchOnMount: 'always',
    });
}

// useChannelQuality 拉取单个渠道的质量概览与趋势。
// 默认关闭自动轮询：质量数据每天才更新一次，实时轮询纯属浪费；
// 卡片展开时按需 refetchOnMount 拉一次即可。
export function useChannelQuality(channelId: number, days = 30, enabled = true) {
    return useQuery({
        queryKey: ['channels', 'quality', channelId, days],
        queryFn: () => apiRequest<ChannelQualitySummary>(`/api/v1/channel/quality/${channelId}?days=${days}`),
        enabled: enabled && channelId > 0,
        refetchOnMount: 'always',
        staleTime: 60_000,
    });
}

// channelGrantListQueryOptions 供分组页查询可选授权。
// 与渠道列表分开: 选取成员只需名称与可用性, 拉整份渠道会连带路径, 代理与凭据明文。
export const channelGrantListQueryOptions = queryOptions({
    queryKey: ['channels', 'grants'],
    queryFn: () => apiRequest<ChannelGrantCandidate[]>('/api/v1/channel/grants'),
});

// useChannelGrantList 获取分组页可选的全部渠道授权。
export function useChannelGrantList(enabled = true) {
    return useQuery({ ...channelGrantListQueryOptions, enabled, refetchOnMount: 'always' });
}

// channelStatsFormattedQueryOptions 统一渠道统计查询, 格式化和刷新策略。
// 与配置分开刷新: 统计每次转发都在变, 配置只在人工改动后由 mutation 失效。
const channelStatsFormattedQueryOptions = queryOptions({
    ...channelStatsQueryOptions,
    select: (data) => data.map((item): ChannelStatsFormatted => ({
        channel_id: item.channel_id,
        user_id: item.user_id,
        channel_name: item.channel_name,
        enabled: item.enabled,
        shared: item.shared,
        share_code: item.share_code,
        review_status: item.review_status,
        review_note: item.review_note,
        submitted_at: item.submitted_at ?? null,
        reviewed_at: item.reviewed_at ?? null,
        trust_level: item.trust_level,
        observation_start: item.observation_start ?? null,
        observation_until: item.observation_until ?? null,
        disabledUntil: item.disabled_until ?? null,
        models: item.models.map((channelModel) => ({
            model_id: channelModel.model_id,
            model_name: channelModel.model_name,
            error_count: channelModel.error_count ?? 0,
            breakerTrips: channelModel.breaker_trips ?? 0,
            disabledUntil: channelModel.disabled_until ?? null,
            lastErrorAt: channelModel.last_error_at ?? null,
            formatted: formatStatsMetrics(channelModel),
        })),
        formatted: formatStatsMetrics(item),
    })),
    refetchInterval: POLL_SLOW,
    refetchOnMount: 'always',
});

// useChannelStats 获取全部渠道及其模型的展示用统计, 也是渠道列表页的数据来源。
export function useChannelStats(enabled = true) {
    return useQuery({ ...channelStatsFormattedQueryOptions, enabled });
}

// ChannelDailyStatsRow 是按天统计的一行: 日期加原始累计指标, 展示侧再计算平均值与成功率。
export type ChannelDailyStatsRow = StatsMetrics & { date: string };

// ChannelModelDailyStats 是单个渠道模型的按天统计序列。
export type ChannelModelDailyStats = {
    channel_model_id: number;
    model_name: string;
    days: ChannelDailyStatsRow[];
};

// ChannelDailyStats 是渠道按天统计的读取形状: 渠道整体与各模型各自的逐日序列。
export type ChannelDailyStats = {
    channel: ChannelDailyStatsRow[];
    models: ChannelModelDailyStats[];
};

// useChannelDailyStats 拉取单个渠道近 14 天的按天统计, 供渠道卡片展示趋势。
export function useChannelDailyStats(channelId: number, enabled = true) {
    return useQuery({
        queryKey: ['channels', 'stats', 'daily', channelId],
        queryFn: () => apiRequest<ChannelDailyStats>(`/api/v1/channel/stats/daily/${channelId}?days=14`),
        enabled: enabled && channelId > 0,
        refetchInterval: POLL_MINUTE,
        refetchOnMount: 'always',
    });
}

/**
 * 获取单个渠道完整配置 Hook, 供编辑表单打开时读取; id 为空时不发请求。
 * 不随统计一并取回: 整份配置带着路径、代理与凭据明文, 只有正在编辑的那一个渠道用得上。
 *
 * @example
 * const { data: detail } = useChannelDetail(channelId);
 */
export function useChannelDetail(id?: number) {
    return useQuery({
        queryKey: ['channels', 'detail', id],
        queryFn: () => apiRequest<ChannelDetail>(`/api/v1/channel/detail/${id}`),
        enabled: id !== undefined,
        refetchOnMount: 'always',
    });
}

/**
 * 创建渠道 Hook；提交整份配置，id 取 0 由后端分配。
 * 授权与凭据、模型在同一请求提交：授权按名称引用两侧，后端在同一事务内解析为主键。
 *
 * @example
 * const createChannel = useCreateChannel();
 *
 * createChannel.mutate({
 *   id: 0,
 *   name: 'OpenAI',
 *   base_url: 'https://api.openai.com',
 *   keys: [{ name: 'default', key: 'sk-xxx', enabled: true }],
 *   models: ['gpt-4o'],
 *   grants: [{ model_name: 'gpt-4o', key_name: 'default', protocols: Protocol.OpenAIResponse }],
 *   // ...其余配置字段
 * });
 */
export function useCreateChannel() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (data: ChannelDetail) =>
            apiRequest<ChannelDetail>('/api/v1/channel/create', { method: 'POST', body: data }),
        onSuccess: () => {
            void queryClient.invalidateQueries({ queryKey: ['channels'] });
            void queryClient.invalidateQueries({ queryKey: modelListQueryOptions.queryKey });
        },
    });
}

/**
 * 更新渠道 Hook；提交整份配置，整体替换。
 * keys、models 和 grants 提交即覆盖：未列出的凭据和模型会被删除并级联删除其授权。
 * grants 的 protocols 不能为 0 或含未定义位，名称也必须属于同一渠道，否则后端整单拒绝。
 *
 * @example
 * const updateChannel = useUpdateChannel();
 *
 * updateChannel.mutate({ ...detail, enabled: false });
 */
export function useUpdateChannel() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (data: ChannelDetail) =>
            apiRequest<ChannelDetail>('/api/v1/channel/update', { method: 'POST', body: data }),
        onSuccess: () => {
            void queryClient.invalidateQueries({ queryKey: ['channels'] });
            void queryClient.invalidateQueries({ queryKey: modelListQueryOptions.queryKey });
            void queryClient.invalidateQueries({ queryKey: groupListQueryOptions.queryKey });
        },
    });
}

/**
 * 删除渠道 Hook
 * 
 * @example
 * const deleteChannel = useDeleteChannel();
 * 
 * deleteChannel.mutate(1); // 删除 ID 为 1 的渠道
 */
export function useDeleteChannel() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (id: number) =>
            apiRequest<null>(`/api/v1/channel/delete/${id}`, { method: 'DELETE' }),
        onSuccess: () => {
            void queryClient.invalidateQueries({ queryKey: ['channels'] });
            void queryClient.invalidateQueries({ queryKey: modelListQueryOptions.queryKey });
            void queryClient.invalidateQueries({ queryKey: groupListQueryOptions.queryKey });
        },
    });
}

// ChannelBreakerResetRequest 是手动解除熔断/额度暂停的提交形状。
// 三种作用域共用一个接口: 凭据(key_names)、渠道模型(model_names)、整条渠道(channel: true)。
export type ChannelBreakerResetRequest = {
    id: number;
    key_names?: string[];
    model_names?: string[];
    // channel 为真表示解除整条渠道的额度暂停, 此时不必给名称; 后端按它跳到渠道级处理。
    channel?: boolean;
};

// resetChannelBreaker 直接调用解除接口, 供 hook 与需要断言请求形状的测试复用。
export function resetChannelBreaker(data: ChannelBreakerResetRequest) {
    return apiRequest<{ reset: number }>('/api/v1/channel/breaker/reset', { method: 'POST', body: data });
}

/**
 * useResetChannelBreaker 手动解除渠道下的熔断, 以及渠道级的额度暂停。
 * 上游修好(换 Key, 补额度)后不必等熔断到期; 名称由后端在渠道范围内匹配, 不存在的名称不会报错。
 * 三种作用域共用一个入口: 凭据(默认)、渠道模型(model_names)、整个渠道(channel: true)。
 * 额度暂停与熔断都写在同一组截止时间上, 解除动作也是同一个, 故复用此封装而不另造一个。
 */
export function useResetChannelBreaker() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: resetChannelBreaker,
        onSuccess: () => {
            // 熔断与额度暂停状态都随统计下发, 解锁后立刻重取, 否则界面还会显示"熔断中"/"额度暂停"。
            void queryClient.invalidateQueries({ queryKey: ['channels'] });
            void queryClient.invalidateQueries({ queryKey: groupListQueryOptions.queryKey });
        },
    });
}

/**
 * 启用/禁用渠道 Hook
 * 
 * @example
 * const enableChannel = useEnableChannel();
 * 
 * enableChannel.mutate({ id: 1, enabled: true }); // 启用 ID 为 1 的渠道
 * enableChannel.mutate({ id: 1, enabled: false }); // 禁用 ID 为 1 的渠道
 */
export function useEnableChannel() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (data: { id: number; enabled: boolean }) =>
            apiRequest<null>('/api/v1/channel/enable', { method: 'POST', body: data }),
        onSuccess: () => {
            void queryClient.invalidateQueries({ queryKey: ['channels'] });
            // 渠道启停会改变其授权在分组内的可用性，成员列表要跟着刷新。
            void queryClient.invalidateQueries({ queryKey: groupListQueryOptions.queryKey });
        },
    });
}

/**
 * 获取渠道模型列表 Hook
 *
 * @example
 * const fetchModel = useFetchModel();
 *
 * fetchModel.mutate({
 *   channel: { base_url: 'https://api.openai.com', openai_response_path: '/v1/responses', ... },
 *   key: 'sk-xxx',
 * });
 *
 * // 在 onSuccess 中获取模型列表
 * fetchModel.data // [{ name: 'gpt-4o', protocols: 4 }, ...]
 */
export function useFetchModel() {
    return useMutation({
        mutationFn: (data: FetchModelRequest) =>
            // 服务端要去上游拉模型列表, 慢渠道可能远超默认 20 秒。
            apiRequest<FetchModel[]>('/api/v1/channel/fetch-model', { method: 'POST', body: data, timeoutMs: 90000 }),
    });
}

// channelReviewInvalidate 审核动作成功后统一失效渠道与审核队列: 两者共享 review_status, 必须同步刷新。
const channelReviewInvalidate = (queryClient: ReturnType<typeof useQueryClient>) => {
    void queryClient.invalidateQueries({ queryKey: ['channels'] });
    void queryClient.invalidateQueries({ queryKey: ['channels', 'review'] });
};

// useChannelReviewList 拉取审核队列: 管理员全量, 渠道商只看自有提交; status 空为全部状态。
export function useChannelReviewList(status?: ChannelReviewStatus) {
    return useQuery({
        queryKey: ['channels', 'review', 'list', status ?? ''],
        queryFn: () => apiRequest<{ items: ChannelReviewItem[] }>(`/api/v1/channel/review/list?status=${status ?? ''}`),
        refetchOnMount: 'always',
    });
}

// useSubmitChannelReview 提交审核: draft|rejected|stale → pending; 管理员免审渠道提交即通过。
export function useSubmitChannelReview() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (data: { id: number; note: string }) =>
            apiRequest<{ id: number; review_status: ChannelReviewStatus }>('/api/v1/channel/review/submit', { method: 'POST', body: data }),
        onSuccess: () => channelReviewInvalidate(queryClient),
    });
}

// useApproveChannelReview 批准审核(管理员): pending → approved 并写配置指纹基线。
export function useApproveChannelReview() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (data: { id: number; note: string }) =>
            apiRequest<{ id: number; review_status: ChannelReviewStatus }>('/api/v1/channel/review/approve', { method: 'POST', body: data }),
        onSuccess: () => channelReviewInvalidate(queryClient),
    });
}

// useRejectChannelReview 驳回审核(管理员): pending → rejected, 原因必填。
export function useRejectChannelReview() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (data: { id: number; note: string }) =>
            apiRequest<{ id: number; review_status: ChannelReviewStatus }>('/api/v1/channel/review/reject', { method: 'POST', body: data }),
        onSuccess: () => channelReviewInvalidate(queryClient),
    });
}

// ChannelProfitDaily 是渠道某一天的利润与计费汇总；daily 只含真正有结算记录的日期。
export type ChannelProfitDaily = {
    date: string; // YYYY-MM-DD
    requests: number;
    user_cost: number; // 收入：用户实付
    owner_revenue: number; // 成本：渠道商供货收入
    platform_revenue: number; // 毛利：收入 - 成本
    margin_rate: number; // 毛利率 0~100，收入为 0 时为 0
};

// ChannelProfitTotals 是区间汇总；字段与单日一致，只是不带日期。
export type ChannelProfitTotals = Omit<ChannelProfitDaily, 'date'>;

// ChannelProfit 是渠道利润接口的完整响应(后端按 settled 计费记录聚合)。
export type ChannelProfit = {
    channel_id: number;
    channel_name: string;
    start_date: string;
    end_date: string;
    totals: ChannelProfitTotals;
    daily: ChannelProfitDaily[];
};

// channelProfitQueryKey 收口查询键：面板与测试共用，日期必须进键，否则换范围不会重新取数。
export function channelProfitQueryKey(channelId: number, startDate: string, endDate: string) {
    return ['channels', 'profit', channelId, startDate, endDate] as const;
}

// channelProfitPath 拼出利润接口的请求路径；日期按查询参数转义，避免空格/特殊字符拼坏 URL。
export function channelProfitPath(channelId: number, startDate: string, endDate: string) {
    return `/api/v1/channel/profit/${channelId}?start_date=${encodeURIComponent(startDate)}&end_date=${encodeURIComponent(endDate)}`;
}

// channelProfitQueryOptions 供面板与测试共享查询定义(webhook 等模块同款做法)。
export function channelProfitQueryOptions(channelId: number, startDate: string, endDate: string) {
    return queryOptions({
        queryKey: channelProfitQueryKey(channelId, startDate, endDate),
        queryFn: ({ signal }) => apiRequest<ChannelProfit>(channelProfitPath(channelId, startDate, endDate), { signal }),
        // 日期为空时不发请求：后端虽有默认区间，但界面此刻还没有可展示的范围。
        enabled: channelId > 0 && Boolean(startDate) && Boolean(endDate),
        refetchOnMount: 'always' as const,
    });
}

// useChannelProfit 拉取单个渠道在日期区间内的收入、成本、毛利与每日趋势。
// 仅管理员可用：非管理员请求会得到 403，面板据此展示无权限提示而不是空数据。
export function useChannelProfit(channelId: number, startDate: string, endDate: string, enabled = true) {
    return useQuery({ ...channelProfitQueryOptions(channelId, startDate, endDate), enabled: enabled && channelId > 0 && Boolean(startDate) && Boolean(endDate) });
}

// ACCOUNT_IMPORT_TIMEOUT_MS 账号导入/刷新的请求超时。
// 后端要在导入时立刻向平台令牌端点刷新一次, 20 秒是后端侧单次刷新超时, 这里留足余量。
const ACCOUNT_IMPORT_TIMEOUT_MS = 30_000;

// 导入的上游账号: 各平台接受的 JSON 字段形状见后端 model/account_json.go 的解析分支。
// 这里只描述「界面要让使用者填什么」, 不做字段校验 —— 校验的真实来源是后端解析器,
// 前端再复制一份规则必然漂移(而且往往漂移在「某个平台多认了一个别名」这种细节上)。
export type AccountPlatform = 'codex' | 'kiro' | 'gemini' | 'grok' | 'claude';

export type AccountImportPayload = {
    channel_id: number;
    platform: AccountPlatform;
    name: string;
    raw: string;
    enabled?: boolean;
    priority?: number;
};

export type AccountImportResult = {
    channel_key_id: number;
    name: string;
    platform: string;
    kind: 'oauth' | 'cookie';
    email?: string;
    expires_at?: string;
    refreshed_now: boolean;
    // auto_renewable=false(cookie 类): 账号失效后无法自动恢复, 只能人工重新导入。
    // 界面必须显式说明, 否则使用者会以为「系统会一直帮我维持」。
    auto_renewable: boolean;
    // warning 表示「账号已导入但首次刷新失败」: 导入是成功的, 不是错误。
    warning?: string;
};

export type AccountRefreshResult = { refreshed: boolean };

// accountImport 导入一份账号 JSON 作为渠道凭据。
// 超时放宽: 后端会在导入时立刻向平台令牌端点刷一次, 验证这份 JSON 到底能不能用。
export function accountImport(payload: AccountImportPayload) {
    return apiRequest<AccountImportResult>('/api/v1/channel/account/import', {
        method: 'POST',
        body: payload,
        timeoutMs: ACCOUNT_IMPORT_TIMEOUT_MS,
    });
}

// AccountListEntry 是账号管理面的一个条目。
//
// 注意它不含账号 JSON 本身(含 access_token/refresh_token): 后端刻意只返回展示所需字段,
// 从结构上不给前端拿到明文的机会。界面若要"看账号内容", 只能去渠道凭据页。
export type AccountListEntry = {
    channel_key_id: number; // 用于"立即刷新"。
    channel_id: number;
    channel_name: string; // 账号归属的渠道, 列表里要显示。
    name: string; // 凭据名, 渠道内唯一。
    enabled: boolean; // 人工开关, 与熔断无关。
    kind: 'oauth' | 'cookie';
    platform: string;
    email: string;
    token_expires_at?: string | null;
    token_refresh_at?: string | null;
    disabled_until?: string | null;
    breaker_trips: number;
    last_error_at?: string | null;
};

// accountList 取当前作用域可见的全部上游账号。
//
// 后端按"最需要注意"排序(熔断 > 已过期 > cookie > 将到期 > 正常), 界面不再重排:
// 两处排序逻辑必然漂移, 而漂移后使用者会看到前后矛盾的顺序。
export function accountList() {
    return apiRequest<AccountListEntry[]>('/api/v1/channel/account/list', { method: 'GET' });
}

// accountRefreshKey 手动刷新某个账号凭据, 用于导入后立刻验证可用性。
export function accountRefreshKey(channelKeyId: number) {
    return apiRequest<AccountRefreshResult>(`/api/v1/channel/account/${channelKeyId}/refresh`, {
        method: 'POST',
        timeoutMs: ACCOUNT_IMPORT_TIMEOUT_MS,
    });
}

// 批量导入相关类型
export type AccountBatchItem = {
    name: string;
    raw: string;
};

export type AccountBatchResult = {
    name: string;
    ok: boolean;
    channel_key_id?: number;
    platform?: string;
    kind?: string;
    error?: string;
};

export type AccountBatchResponse = {
    total: number;
    succeeded: number;
    failed: number;
    results: AccountBatchResult[];
};

// accountImportBatch 批量导入多个账号到同一渠道。
//
// 后端刻意不做首刷: 逐条首刷会 N 次上游调用, 又慢又可能触发风控;
// 批量的语义是快速录入, 验证留给账号页统一刷新。
export function accountImportBatch(payload: {
    channel_id: number;
    platform: AccountPlatform;
    items: AccountBatchItem[];
    enabled?: boolean;
}) {
    return apiRequest<AccountBatchResponse>('/api/v1/channel/account/import-batch', {
        method: 'POST',
        body: payload,
        timeoutMs: ACCOUNT_IMPORT_TIMEOUT_MS,
    });
}
