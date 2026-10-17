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
};

export class ApiError extends Error {
    status: number;

    constructor(status: number, message: string) {
        super(message);
        this.name = 'ApiError';
        this.status = status;
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

// 相对于当前页面目录发送 API 请求，保留反向代理的子路径前缀。
export async function apiRequest<T>(path: string, options: RequestOptions = {}): Promise<T> {
    const headers = new Headers(options.body === undefined ? undefined : { 'Content-Type': 'application/json' });
    if (apiKey) headers.set('Authorization', `Bearer ${apiKey}`);

    // 超时与调用方取消合并到一个 controller: 谁先触发谁生效。
    // 不用 AbortSignal.any: 兼容性更稳, 也便于区分「超时」与「调用方主动取消」。
    const controller = new AbortController();
    let timedOut = false;
    const timer = globalThis.setTimeout(() => {
        timedOut = true;
        controller.abort();
    }, options.timeoutMs ?? apiTimeoutMs);
    const forwardAbort = () => controller.abort();
    options.signal?.addEventListener('abort', forwardAbort, { once: true });
    if (options.signal?.aborted) controller.abort();

    let response: Response;
    try {
        response = await fetch(new URL(`.${path}`, document.baseURI), {
            method: options.method ?? 'GET',
            headers,
            body: options.body === undefined ? undefined : JSON.stringify(options.body),
            credentials: 'include',
            signal: controller.signal,
        });
    } catch (error) {
        if (timedOut) throw new ApiError(408, 'Request timed out');
        throw error; // 调用方取消(如组件卸载)保持原样抛出, 由上层忽略
    } finally {
        globalThis.clearTimeout(timer);
        options.signal?.removeEventListener('abort', forwardAbort);
    }
    const data = await response.json().catch(() => null) as { message?: string; data?: T } | null;
    if (!response.ok) {
        if (response.status === 401 && options.dispatchUnauthorized !== false && typeof window !== 'undefined') {
            window.dispatchEvent(new Event(apiUnauthorizedEvent));
        }
        throw new ApiError(response.status, data?.message || `Request failed: ${response.status}`);
    }
    return data?.data as T;
}
