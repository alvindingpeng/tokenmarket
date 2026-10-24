import { QueryClient } from '@tanstack/react-query';

// queryClient 管理整个前端应用共享的查询缓存和请求状态。
export const queryClient = new QueryClient({
    defaultOptions: {
        queries: {
            staleTime: 60 * 1000,
            refetchOnWindowFocus: false,
        },
    },
});

type RequestOptions = {
    method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';
    body?: unknown;
    dispatchUnauthorized?: boolean;
    signal?: AbortSignal;
    // timeoutMs 覆盖默认超时; 少数服务端会去探测上游的接口(拉模型列表/健康检查/备份)需要放宽。
    timeoutMs?: number;
    // skipStepUp 关掉本请求的二次验证交互: 换 step-up 凭证的请求自身不能再弹一次密码框。
    skipStepUp?: boolean;
};

export class ApiError extends Error {
    status: number;
    // code 是服务端的机器可读错误分类(目前只有 step_up_required), 不要用文案去猜错误类型。
    code?: string;

    constructor(status: number, message: string, code?: string) {
        super(message);
        this.name = 'ApiError';
        this.status = status;
        this.code = code;
    }
}

export const apiUnauthorizedEvent = 'api:unauthorized';

// apiTimeoutMs 单次请求的兜底超时。上游卡住时让调用方拿到明确失败,
// 而不是一个永远转圈、按钮永久 pending 的加载态。
const apiTimeoutMs = 20000;

let apiKey: string | null = null;

export function setAPIKey(value: string | null) {
    apiKey = value;
}

// ---------------------------------------------------------------------------
// 高危操作二次验证(step-up)
//
// 后端对高危写操作(余额调整/提现审批/备份导入/敏感导出/结算生成/计费恢复/系统设置/用户管理)
// 会回 428 + error_code=step_up_required, 表示"这次请求在进业务处理前就被拦下了"。
// 这里只对这一种明确错误弹密码确认, 确认后把原请求原样重发一次(带 X-Step-Up-Token);
// 其它任何错误(超时/5xx/校验失败/401)一律照常抛给调用方, 既不弹窗也不重发 ——
// 重发只发生在"服务端明确说没执行过"的请求上, 因此不会把已执行的交易重复执行。
// ---------------------------------------------------------------------------

export const stepUpErrorCode = 'step_up_required';
export const stepUpTokenHeader = 'X-Step-Up-Token';
export const stepUpVerifyPath = '/api/v1/user/step-up';

export function isStepUpRequired(error: unknown): boolean {
    return error instanceof ApiError && error.code === stepUpErrorCode;
}

// StepUpHandler 弹窗实现: 走完"输密码 -> 换凭证"的交互, 成功返回凭证;
// 用户取消(或没有弹窗可弹)返回 null。返回 null 时调用方原样抛出 step-up 错误。
export type StepUpHandler = () => Promise<string | null>;

// stepUpHandler 由 <StepUpDialog> 注册; 没注册时(纯逻辑环境/测试)不弹窗, 直接报错。
let stepUpHandler: StepUpHandler | null = null;
// stepUpToken 只存在内存里, 不进 storage: 页面刷新就要重新确认密码, 被盗走的脚本也读不到长期凭证。
// 它本身由服务端绑定了用户 + token_version + 当前登录会话, 换会话/改密即失效。
let stepUpToken: string | null = null;
// pendingStepUp 并发请求同时撞上 step-up 时共用一次弹窗, 不叠一摞密码框。
let pendingStepUp: Promise<string | null> | null = null;
let stepUpGeneration = 0;

export function setStepUpHandler(handler: StepUpHandler | null) {
    stepUpHandler = handler;
}

export function clearStepUpToken() {
    stepUpGeneration += 1;
    stepUpToken = null;
    pendingStepUp = null;
}

export function currentStepUpToken(): string | null {
    return stepUpToken;
}

function acquireStepUpToken(): Promise<string | null> {
    if (!stepUpHandler) return Promise.resolve(null);
    if (!pendingStepUp) {
        const generation = stepUpGeneration;
        pendingStepUp = stepUpHandler()
            .then((token) => {
                if (generation !== stepUpGeneration) return null;
                stepUpToken = token && token.length > 0 ? token : null;
                return stepUpToken;
            })
            .catch(() => {
                if (generation !== stepUpGeneration) return null;
                stepUpToken = null;
                return null;
            })
            .finally(() => {
                if (generation === stepUpGeneration) pendingStepUp = null;
            });
    }
    return pendingStepUp;
}

// verifyStepUpPassword 用登录密码换一枚短期 step-up 凭证。
// 401 在这里表示"口令不对"而不是登录失效, 因此不广播 api:unauthorized(否则会把用户踢下线)。
//
// 注意它只换不存: 凭证是否入库由 acquireStepUpToken 决定 —— 只有"那次确认还被等着"时
// 凭证才有意义。弹窗被取消后才迟到的验证响应, 换来的凭证必须整体作废, 否则一次取消
// 就可能被迟到响应悄悄盖章给后一个请求。
export async function verifyStepUpPassword(password: string): Promise<string> {
    const data = await apiRequest<{ token?: string; expires_in?: number }>(stepUpVerifyPath, {
        method: 'POST',
        body: { password },
        dispatchUnauthorized: false,
        skipStepUp: true,
    });
    const token = data?.token;
    if (!token) throw new ApiError(502, 'step-up verification returned no credential');
    return token;
}

// ---------------------------------------------------------------------------
// 请求内核
// ---------------------------------------------------------------------------

type RawRequest = {
    method: string;
    body?: BodyInit;
    contentType?: string;
    dispatchUnauthorized?: boolean;
    signal?: AbortSignal;
    timeoutMs?: number;
};

type SendOutcome =
    | { ok: true; response: Response }
    | { ok: false; error: ApiError };

// resolveAPIURL 拼出请求地址。以 '/...' 开头的是 apiRequest 的老约定: 相对当前页面目录,
// 保留反向代理的子路径前缀; 已经是 './...' 的(导出 URL 构造器的产物)原样相对页面解析,
// 不再补前缀, 免得拼出 '..//api/...' 这种路径。
function resolveAPIURL(path: string): URL {
    const target = path.startsWith('./') || /^[a-z]+:/i.test(path) ? path : `.${path}`;
    return new URL(target, document.baseURI);
}

// sendOnce 执行一次 HTTP 往返: 超时/取消语义在这里, 也在这里把错误外壳解析成 ApiError。
// 成功响应的 body 不动, 留给调用方(json/blob/text 各取所需)。
async function sendOnce(path: string, raw: RawRequest): Promise<SendOutcome> {
    const headers = new Headers();
    if (raw.contentType && raw.body !== undefined) headers.set('Content-Type', raw.contentType);
    if (apiKey) headers.set('Authorization', `Bearer ${apiKey}`);
    if (stepUpToken) headers.set(stepUpTokenHeader, stepUpToken);

    // 超时与调用方取消合并到一个 controller: 谁先触发谁生效。
    // 不用 AbortSignal.any: 兼容性更稳, 也便于区分「超时」与「调用方主动取消」。
    const controller = new AbortController();
    let timedOut = false;
    const timer = globalThis.setTimeout(() => {
        timedOut = true;
        controller.abort();
    }, raw.timeoutMs ?? apiTimeoutMs);
    const forwardAbort = () => controller.abort();
    raw.signal?.addEventListener('abort', forwardAbort, { once: true });
    if (raw.signal?.aborted) controller.abort();

    let response: Response;
    try {
        response = await fetch(resolveAPIURL(path), {
            method: raw.method,
            headers,
            body: raw.body,
            credentials: 'include',
            signal: controller.signal,
        });
    } catch (error) {
        if (timedOut) throw new ApiError(408, 'Request timed out');
        throw error; // 调用方取消(如组件卸载)保持原样抛出, 由上层忽略
    } finally {
        globalThis.clearTimeout(timer);
        raw.signal?.removeEventListener('abort', forwardAbort);
    }
    if (response.ok) return { ok: true, response };

    const payload = await response.json().catch(() => null) as { message?: string; error_code?: string } | null;
    if (response.status === 401 && raw.dispatchUnauthorized !== false) {
        clearStepUpToken();
        if (typeof window !== 'undefined') window.dispatchEvent(new Event(apiUnauthorizedEvent));
    }
    return { ok: false, error: new ApiError(response.status, payload?.message || `Request failed: ${response.status}`, payload?.error_code) };
}

// sendWithStepUp 带二次验证交互的往返: 只有"明确的 step-up 错误"才弹密码确认并原样重发一次。
// 重发的结果就是最终结果 —— 成功返回, 失败按失败抛出, 绝不第二次弹窗、绝不第三次请求。
async function sendWithStepUp(path: string, raw: RawRequest, allowStepUp: boolean): Promise<SendOutcome> {
    const generation = stepUpGeneration;
    const first = await sendOnce(path, raw);
    if (generation !== stepUpGeneration) return first;
    if (first.ok || !allowStepUp || !isStepUpRequired(first.error)) return first;

    // 走到这里说明旧凭证已被服务端判失效(过期/改密/换会话), 丢掉它重新确认一次。
    stepUpToken = null;
    const token = await acquireStepUpToken();
    if (!token || generation !== stepUpGeneration) return first; // 取消或会话已变更, 不再请求
    if (raw.signal?.aborted) throw new DOMException("Request aborted", "AbortError");
    return sendOnce(path, raw);
}

// ---------------------------------------------------------------------------
// 对外请求形态: JSON API、文件下载、表单上传
// ---------------------------------------------------------------------------

// 相对于当前页面目录发送 API 请求，保留反向代理的子路径前缀。
export async function apiRequest<T>(path: string, options: RequestOptions = {}): Promise<T> {
    const outcome = await sendWithStepUp(
        path,
        {
            method: options.method ?? 'GET',
            body: options.body === undefined ? undefined : JSON.stringify(options.body),
            contentType: options.body === undefined ? undefined : 'application/json',
            dispatchUnauthorized: options.dispatchUnauthorized,
            signal: options.signal,
            timeoutMs: options.timeoutMs,
        },
        options.skipStepUp !== true,
    );
    if (!outcome.ok) throw outcome.error;
    const data = await outcome.response.json().catch(() => null) as { message?: string; data?: T } | null;
    return data?.data as T;
}

// filenameFromDisposition 从下载响应头取服务端给的文件名。
function filenameFromDisposition(value: string | null): string | null {
    if (!value) return null;
    const match = value.match(/filename="([^"]+)"/i);
    return match?.[1] ?? null;
}

export type DownloadResult = { blob: Blob; filename: string | null };

// apiDownload 拉文件(整库导出/各类敏感 CSV): 走同一套 step-up 交互,
// 文件本体经 fetch+blob 下载 —— a[href]/window.open 带不了 X-Step-Up-Token 头。
export async function apiDownload(path: string, options: { timeoutMs?: number; signal?: AbortSignal } = {}): Promise<DownloadResult> {
    const outcome = await sendWithStepUp(path, { method: 'GET', timeoutMs: options.timeoutMs, signal: options.signal }, true);
    if (!outcome.ok) throw outcome.error;
    return {
        blob: await outcome.response.blob(),
        filename: filenameFromDisposition(outcome.response.headers.get('content-disposition')),
    };
}

// saveBlob 把下载到的内存 blob 交给浏览器落盘。
export function saveBlob(blob: Blob, filename: string) {
    const url = URL.createObjectURL(blob);
    try {
        const a = document.createElement('a');
        a.href = url;
        a.download = filename;
        document.body.appendChild(a);
        a.click();
        a.remove();
    } finally {
        URL.revokeObjectURL(url);
    }
}

// downloadFile 下载并保存; 返回实际落盘的文件名。失败原样抛出, 由调用方提示。
export async function downloadFile(path: string, fallbackFilename: string, options: { timeoutMs?: number } = {}): Promise<string> {
    const { blob, filename } = await apiDownload(path, options);
    const name = filename || fallbackFilename;
    saveBlob(blob, name);
    return name;
}

// apiUpload 上传表单(备份导入): FormData 由浏览器自己带 boundary, 不能手写 Content-Type。
export async function apiUpload<T>(path: string, form: FormData, options: { timeoutMs?: number; signal?: AbortSignal } = {}): Promise<T> {
    const outcome = await sendWithStepUp(path, { method: 'POST', body: form, timeoutMs: options.timeoutMs, signal: options.signal }, true);
    if (!outcome.ok) throw outcome.error;
    const text = await outcome.response.text().catch(() => '');
    let payload: unknown = text;
    try {
        payload = JSON.parse(text);
    } catch {
        // 后端偶尔回纯文本(如代理层的兜底页), 原样交给调用方。
    }
    if (payload && typeof payload === 'object' && 'data' in payload) {
        return (payload as { data: T }).data;
    }
    return payload as T;
}
