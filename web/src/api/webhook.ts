import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiQueryRetry, apiRequest } from './client';
import type { NotificationType } from './notification';

// ---------------------------------------------------------------------------
// Webhook 订阅(P0-5)
//
// 仅通用 JSON 签名 Webhook: 后端按 JSON 载荷 + 签名头投递, 不做钉钉/飞书等
// 专有格式适配, 界面文案也不做兼容性暗示。路径/字段已与 internal/model/webhook.go
// (WebhookView/WebhookDeliveryLogView/WebhookTestResult)核对一致, 对齐时只改这一层:
//   POST   /api/v1/webhook                 {url,secret,events,enabled,retry_count}
//   GET    /api/v1/webhook/list?page&page_size      -> {items,total}
//   PUT    /api/v1/webhook/:id             {url,secret?,events,enabled,retry_count}
//   DELETE /api/v1/webhook/:id
//   POST   /api/v1/webhook/:id/test        -> queued(异步入队, 不是发送成功)
//   GET    /api/v1/webhook/:id/logs?page&page_size&status&event -> {items,total}
//   POST   /api/v1/webhook/:id/deliveries/:delivery_id/replay   -> {delivery}
//
// 安全红线(本层与界面共同遵守):
//   - 密钥只进不出: has_secret 是唯一回读信号, 任何接口都不会带回密钥明文;
//   - URL 可能带 token, 绝不进 console/toast, 报错文案经 redactWebhookError 抹除;
//   - 投递日志只消费状态/HTTP 码/耗时/尝试次数, 不读不渲染任何响应体(HTML)。
// ---------------------------------------------------------------------------

// WebhookEvent 可订阅事件集合就是站内通知类型(与 model.NotificationType 一一对应)。
export type WebhookEvent = NotificationType;
export const WEBHOOK_EVENTS: WebhookEvent[] = [
    'system',
    'balance_low',
    'ratelimit_hit',
    'channel_down',
    'channel_review',
    'withdrawal',
    'recharge',
    'dispute',
    'ticket',
];

// 投递状态闭集(与 model.WebhookDeliveryStatus 同词): 筛选下拉与「能否重放」都从这里取,
// 不散落在组件里, 免得后端加状态时界面各处口径不一致。
export const WEBHOOK_DELIVERY_STATUSES = ['pending', 'processing', 'succeeded', 'failed', 'cancelled'] as const;
export type WebhookDeliveryStatus = (typeof WEBHOOK_DELIVERY_STATUSES)[number];

// 投递日志可选的事件筛选值: 订阅事件闭集 + test。
// test 是「测试发送」, 不在订阅事件里, 但日志里会出现 —— 少了它就没法只看测试投递。
export const WEBHOOK_LOG_EVENT_FILTERS: string[] = [...WEBHOOK_EVENTS, 'test'];

// WebhookLogFilter 日志筛选条件: 空串/缺省都表示「不筛选」。
// 空串绝不能拼进 query string —— 后端会把 status= 当成一个不存在的状态值, 结果是永远空列表。
export type WebhookLogFilter = { status?: string; event?: string };

// isWebhookEvent 判断事件名是否在已知枚举内; 后端新增事件名原样展示, 不硬拼文案键(缺键会白屏)。
export function isWebhookEvent(value: string): value is WebhookEvent {
    return (WEBHOOK_EVENTS as string[]).includes(value);
}

/**
 * WebhookView 订阅视图(与暂定契约的 Webhook 对齐)。
 * 注意本类型刻意没有 secret 字段: 密钥只进不出, has_secret 只回答「设没设」。
 * last_status 是最近一次投递的状态词(取值后端对齐), 空表示还没有投递记录。
 */
export type WebhookView = {
    id: number;
    url: string;
    events: string[];
    enabled: boolean;
    retry_count: number;
    has_secret: boolean;
    created_at: string;
    updated_at: string;
    last_status?: string | null;
};

/**
 * WebhookDeliveryLog 一条投递日志(字段名与 model.WebhookDeliveryLogView 对齐)。
 * 只描述投递元数据; 响应正文后端永不落库, 界面也不渲染任何响应内容。
 * delivery_id 是载荷里给接收方去重用的投递 ID(至少一次投递语义)。
 * 日志字段严格与后端视图对齐: 包含响应码和最近一次耗时, 不包含响应正文。
 * 全部服务端字段按可缺省处理, 缺字段显示占位符而不是猜值。
 */
export type WebhookDeliveryLog = {
    delivery_id?: string;
    event?: string;
    status?: string;
    attempts?: number;
    response_code?: number;
    duration_ms?: number;
    error?: string;
    created_at?: string;
    updated_at?: string;
};

export type WebhookListResponse = { items: WebhookView[]; total: number };
export type WebhookLogListResponse = { items: WebhookDeliveryLog[]; total: number };

// 分页大小: 列表与投递日志各自独立翻页, 后端上限 100, 前端统一 20 一页(与工单一致)。
export const WEBHOOK_PAGE_SIZE = 20;
export const WEBHOOK_LOG_PAGE_SIZE = 20;

// 密钥字节长度约束: 创建必填 16..128 字节(按 UTF-8 字节数, 不是字符数);
// 编辑留空表示保持原密钥。retry_count 取 0..3。
export const WEBHOOK_SECRET_MIN_BYTES = 16;
export const WEBHOOK_SECRET_MAX_BYTES = 128;
export const WEBHOOK_RETRY_MIN = 0;
export const WEBHOOK_RETRY_MAX = 3;
// WEBHOOK_URL_MAX_LEN 地址长度上限, 与 model.WebhookURLMaxLen 一致。
export const WEBHOOK_URL_MAX_LEN = 2048;

// ---------------------------------------------------------------------------
// 请求路径: 显式 /api/v1 前缀, 少了前缀就是 404(此前多处踩过)。
// ---------------------------------------------------------------------------

export const webhookCollectionPath = '/api/v1/webhook';

/** webhookListPath 订阅列表: /api/v1/webhook/list?page=1&page_size=20 */
export function webhookListPath(page: number, pageSize: number): string {
    const query = new URLSearchParams({
        page: String(Math.max(1, page)),
        page_size: String(pageSize),
    });
    return webhookCollectionPath + '/list?' + query.toString();
}

/** webhookItemPath 单个订阅: /api/v1/webhook/7(更新/删除共用) */
export function webhookItemPath(id: number): string {
    return webhookCollectionPath + '/' + String(id);
}

/** webhookTestPath 测试投递入队: /api/v1/webhook/7/test */
export function webhookTestPath(id: number): string {
    return webhookCollectionPath + '/' + String(id) + '/test';
}

/**
 * webhookLogsPath 投递日志: /api/v1/webhook/7/logs?page=1&page_size=20[&status=failed][&event=ticket]
 * status/event 只在有值时才拼: 空串代表「全部」, 拼成空参数会被后端当成一个不存在的取值。
 */
export function webhookLogsPath(id: number, page: number, pageSize: number, filter: WebhookLogFilter = {}): string {
    const query = new URLSearchParams({
        page: String(Math.max(1, page)),
        page_size: String(pageSize),
    });
    if (filter.status) query.set('status', filter.status);
    if (filter.event) query.set('event', filter.event);
    return webhookCollectionPath + '/' + String(id) + '/logs?' + query.toString();
}

/**
 * webhookReplayPath 重放一条投递: /api/v1/webhook/7/deliveries/dlv-1/replay
 * delivery_id 编码后再拼路径: 它是载荷里给接收方去重的字符串, 含 / 或空格时
 * 原样拼接会把路径切碎, 变成另一个(不存在的)接口。
 */
export function webhookReplayPath(webhookId: number, deliveryId: string): string {
    return webhookItemPath(webhookId) + '/deliveries/' + encodeURIComponent(deliveryId) + '/replay';
}

// ---------------------------------------------------------------------------
// 输入校验(纯函数, 服务端校验才是权威, 这里只为即时反馈)
// ---------------------------------------------------------------------------

export type WebhookUrlCheck = 'ok' | 'empty' | 'invalid' | 'httpsOnly' | 'port';

// checkWebhookUrl 地址校验: 只允许 https 且端口为空(默认 443)或显式 443 ——
// 与服务端「仅 HTTPS 公网 443」策略同向。公网属性由服务端判定, 前端不猜 IP 段。
export function checkWebhookUrl(raw: string): WebhookUrlCheck {
    const value = raw.trim();
    if (value === '') return 'empty';
    let parsed: URL;
    try {
        parsed = new URL(value);
    } catch {
        return 'invalid';
    }
    if (parsed.protocol !== 'https:') return 'httpsOnly';
    if (parsed.port !== '' && parsed.port !== '443') return 'port';
    return 'ok';
}

/** webhookSecretByteLength 密钥的 UTF-8 字节数: 「16..128 字节」按字节算, 中文一个字符 3 字节。 */
export function webhookSecretByteLength(secret: string): number {
    return new TextEncoder().encode(secret).length;
}

export type WebhookSecretCheck = 'ok' | 'required' | 'tooShort' | 'tooLong';

// checkWebhookSecret 密钥校验: 创建必填, 编辑留空=保持原密钥; 非空时统一 16..128 字节。
export function checkWebhookSecret(secret: string, mode: 'create' | 'edit'): WebhookSecretCheck {
    if (secret === '') return mode === 'create' ? 'required' : 'ok';
    const bytes = webhookSecretByteLength(secret);
    if (bytes < WEBHOOK_SECRET_MIN_BYTES) return 'tooShort';
    if (bytes > WEBHOOK_SECRET_MAX_BYTES) return 'tooLong';
    return 'ok';
}

// redactWebhookError 把错误文案里出现的 URL/密钥原样抹掉再给 toast 展示:
// URL 可能带 token, 密钥是写后不读的凭据, 两者都不允许经错误透出到界面通知。
export function redactWebhookError(message: string, sensitive: Array<string | null | undefined>): string {
    let out = message;
    for (const value of sensitive) {
        if (value) out = out.split(value).join('***');
    }
    return out;
}

// ---------------------------------------------------------------------------
// 查询: queryKey 以 userId 打头隔离身份缓存 —— 换账号不复用上一个账号的订阅数据。
// queryFn 接 react-query 的 signal(卸载即中止在飞请求), 4xx 不重试(见 apiQueryRetry)。
// ---------------------------------------------------------------------------

export function webhookListQueryOptions(userId: number, page: number, pageSize = WEBHOOK_PAGE_SIZE) {
    return {
        queryKey: ['webhook', 'list', userId, page, pageSize] as const,
        queryFn: ({ signal }: { signal: AbortSignal }) =>
            apiRequest<WebhookListResponse>(webhookListPath(page, pageSize), { signal }),
        retry: apiQueryRetry,
        // 翻页保留上一页数据, 不闪回加载态(与工单列表同款交互)。
        placeholderData: (previous: WebhookListResponse | undefined) => previous,
    };
}

export function webhookLogsQueryOptions(
    userId: number,
    id: number | null,
    page: number,
    pageSize = WEBHOOK_LOG_PAGE_SIZE,
    filter: WebhookLogFilter = {},
) {
    // 空串归一成空串语义: '' 与 undefined 都是「不筛选」, 必须算出同一个 key,
    // 否则同义状态会各拉一次请求(还可能让 placeholderData 误判成换页)。
    const status = filter.status || '';
    const event = filter.event || '';
    return {
        // status/event 必须进 queryKey: TanStack Query 靠 key 判缓存命中和失效,
        // key 里不含筛选就会「改了筛选但列表不动」—— 点了没反应比报错更难查。
        queryKey: ['webhook', 'logs', userId, id, page, pageSize, status, event] as const,
        queryFn: ({ signal }: { signal: AbortSignal }) =>
            apiRequest<WebhookLogListResponse>(
                webhookLogsPath(id as number, page, pageSize, { status, event }),
                { signal },
            ),
        retry: apiQueryRetry,
        enabled: id !== null,
        placeholderData: (previous: WebhookLogListResponse | undefined) => previous,
    };
}

export function useWebhookList(userId: number, page: number) {
    return useQuery(webhookListQueryOptions(userId, page));
}

export function useWebhookLogs(userId: number, id: number | null, page: number, filter: WebhookLogFilter = {}) {
    return useQuery(webhookLogsQueryOptions(userId, id, page, WEBHOOK_LOG_PAGE_SIZE, filter));
}

// ---------------------------------------------------------------------------
// 写操作
// ---------------------------------------------------------------------------

/** WebhookWritePayload 创建/编辑请求体。secret 创建必填; 编辑非空才上行(空=保持原密钥)。 */
export type WebhookWritePayload = {
    url: string;
    events: string[];
    enabled: boolean;
    retry_count: number;
    secret?: string;
};

export type WebhookRequestOptions = { signal?: AbortSignal };

// createWebhook 创建订阅; secret 必带(服务端 16..128 字节校验)。
export function createWebhook(payload: WebhookWritePayload, opts: WebhookRequestOptions = {}): Promise<WebhookView> {
    return apiRequest<WebhookView>(webhookCollectionPath, { method: 'POST', body: payload, signal: opts.signal });
}

// updateWebhook 编辑订阅。空 secret 不上行: 「留空保持原密钥」用字段缺省表达,
// 免得空串被后端误读成「把密钥清空」(契约对齐点, 若后端定为显式空串语义再改这里)。
export function updateWebhook(id: number, payload: WebhookWritePayload, opts: WebhookRequestOptions = {}): Promise<WebhookView> {
    const body: WebhookWritePayload = {
        url: payload.url,
        events: payload.events,
        enabled: payload.enabled,
        retry_count: payload.retry_count,
    };
    if (payload.secret) body.secret = payload.secret;
    return apiRequest<WebhookView>(webhookItemPath(id), { method: 'PUT', body, signal: opts.signal });
}

// deleteWebhook 删除订阅。
export function deleteWebhook(id: number, opts: WebhookRequestOptions = {}): Promise<{ deleted: boolean }> {
    return apiRequest<{ deleted: boolean }>(webhookItemPath(id), { method: 'DELETE', signal: opts.signal });
}

/**
 * WebhookTestResult 测试发送的入队回执(与 model.WebhookTestResult 对齐):
 * delivery_id 是本次测试投递的 ID, status 是入队状态 —— 只代表已入队, 不代表远端已收到。
 */
export type WebhookTestResult = { delivery_id: string; status: string };

// requestWebhookTest 请求一次测试投递。接口只回答「已入队」——
// 异步投递的结果在投递日志里看, 界面不许声称「发送成功」。
export function requestWebhookTest(id: number, opts: WebhookRequestOptions = {}): Promise<WebhookTestResult> {
    return apiRequest<WebhookTestResult>(webhookTestPath(id), { method: 'POST', signal: opts.signal });
}

// useInvalidateWebhooks 写操作后失效订阅缓存: key 里的 userId 已隔离身份,
// 失效整棵 webhook 子树只影响当前用户挂载着的查询。
function useInvalidateWebhooks() {
    const queryClient = useQueryClient();
    return () => void queryClient.invalidateQueries({ queryKey: ['webhook'] });
}

type WriteInput = WebhookWritePayload & WebhookRequestOptions;

export function useCreateWebhook() {
    const invalidate = useInvalidateWebhooks();
    return useMutation({
        mutationFn: (input: WriteInput) => {
            const { signal, ...payload } = input;
            return createWebhook(payload, { signal });
        },
        onSuccess: invalidate,
    });
}

export function useUpdateWebhook() {
    const invalidate = useInvalidateWebhooks();
    return useMutation({
        mutationFn: (input: WriteInput & { id: number }) => {
            const { id, signal, ...payload } = input;
            return updateWebhook(id, payload, { signal });
        },
        onSuccess: invalidate,
    });
}

export function useDeleteWebhook() {
    const invalidate = useInvalidateWebhooks();
    return useMutation({
        mutationFn: (input: { id: number } & WebhookRequestOptions) => {
            const { id, signal } = input;
            return deleteWebhook(id, { signal });
        },
        onSuccess: invalidate,
    });
}

export function useTestWebhook() {
    // 测试投递只入队, 不改订阅数据; 日志查询已被观察到, 不主动失效。
    return useMutation({
        mutationFn: (input: { id: number } & WebhookRequestOptions) => {
            const { id, signal } = input;
            return requestWebhookTest(id, { signal });
        },
    });
}

// ---------------------------------------------------------------------------
// 重放(POST /api/v1/webhook/:id/deliveries/:delivery_id/replay)
//
// 重放是把原投递重新入队, 不是「重发成功」; 结果仍要回来投递日志里看。
// 只有终点状态(failed/cancelled)允许重放: 在飞的重放会重复投递, 已成功的不需要。
// ---------------------------------------------------------------------------

/** WebhookReplayResponse 重放回执: 与原投递同构的日志视图(状态通常回到 pending)。 */
export type WebhookReplayResponse = { delivery: WebhookDeliveryLog };

// canReplayDelivery 该状态是否可重放。状态词与 model.WebhookDeliveryStatus 同词;
// 未知状态一律按不可重放处理(界面上置灰比允许调用一个必然 409 的接口更诚实)。
export function canReplayDelivery(status?: string | null): boolean {
    return status === 'failed' || status === 'cancelled';
}

// replayDelivery 重放一次投递。404/409 让 apiRequest 原样抛出, 由界面给通用失败文案。
export async function replayDelivery(
    webhookId: number,
    deliveryId: string,
    opts: WebhookRequestOptions = {},
): Promise<WebhookDeliveryLog> {
    const response = await apiRequest<WebhookReplayResponse>(
        webhookReplayPath(webhookId, deliveryId),
        { method: 'POST', signal: opts.signal },
    );
    return response.delivery;
}

export function useReplayDelivery() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (input: { webhookId: number; deliveryId: string } & WebhookRequestOptions) => {
            const { webhookId, deliveryId, signal } = input;
            return replayDelivery(webhookId, deliveryId, { signal });
        },
        // 重放会新增一条投递记录并刷新原记录状态: 失效日志子树让当前列表重新拉取。
        // 用前缀 key 失效, 命中该订阅下所有筛选/翻页组合, 不只当前这一页。
        onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['webhook', 'logs'] }),
    });
}
