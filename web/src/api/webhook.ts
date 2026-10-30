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
//   GET    /api/v1/webhook/:id/logs?page&page_size   -> {items,total}
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

/** webhookLogsPath 投递日志: /api/v1/webhook/7/logs?page=1&page_size=20 */
export function webhookLogsPath(id: number, page: number, pageSize: number): string {
    const query = new URLSearchParams({
        page: String(Math.max(1, page)),
        page_size: String(pageSize),
    });
    return webhookCollectionPath + '/' + String(id) + '/logs?' + query.toString();
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

export function webhookLogsQueryOptions(userId: number, id: number | null, page: number, pageSize = WEBHOOK_LOG_PAGE_SIZE) {
    return {
        queryKey: ['webhook', 'logs', userId, id, page, pageSize] as const,
        queryFn: ({ signal }: { signal: AbortSignal }) =>
            apiRequest<WebhookLogListResponse>(webhookLogsPath(id as number, page, pageSize), { signal }),
        retry: apiQueryRetry,
        enabled: id !== null,
        placeholderData: (previous: WebhookLogListResponse | undefined) => previous,
    };
}

export function useWebhookList(userId: number, page: number) {
    return useQuery(webhookListQueryOptions(userId, page));
}

export function useWebhookLogs(userId: number, id: number | null, page: number) {
    return useQuery(webhookLogsQueryOptions(userId, id, page));
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
