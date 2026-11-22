import { apiRequest } from './client';

export type TOTPSetup = { secret: string; otpauth_url: string };
// Use plain requests, not mutation/query caches: setup secrets and recovery codes
// must only live in the active UI, never in persisted browser state.
export function setupTOTP(signal?: AbortSignal) {
    return apiRequest<TOTPSetup>('/api/v1/user/2fa/setup', { method: 'POST', signal });
}
export function enableTOTP(code: string, signal?: AbortSignal) {
    return apiRequest<{ recovery_codes: string[] }>('/api/v1/user/2fa/enable', {
        method: 'POST', body: { code }, signal,
    });
}
export function disableTOTP(code: string, signal?: AbortSignal) {
    return apiRequest('/api/v1/user/2fa/disable', { method: 'POST', body: { code }, signal });
}

// TrustedDevice 一台"30 天内免输动态码"的浏览器(与 model.TrustedDeviceView 对齐)。
//
// 字段只有展示用的信息: 后端**从不**下发令牌哈希或明文令牌, 因此这里也不该有。令牌本身活在
// HttpOnly Cookie 里, JS 看不见 —— 这正是安全的地方(任何一处 XSS 都换不到免码能力)。
export type TrustedDevice = {
    id: number;
    user_agent: string;
    client_ip: string;
    created_at: string;
    last_used_at: string;
    expires_at: string;
    // expired 由服务端算好: 前端不该拿本地时钟跟 UTC 时间戳比(时区/时钟漂移会算反)。
    expired: boolean;
};

// listTrustedDevices 只读清单: 不需要 step-up(看一眼自己有几台设备不该弹密码确认)。
export function listTrustedDevices(signal?: AbortSignal) {
    return apiRequest<{ items: TrustedDevice[] }>('/api/v1/user/2fa/trusted-devices', { signal });
}

// revokeTrustedDevice / revokeAllTrustedDevices 是安全写: 服务端要求 step-up 凭证。
// apiRequest 在收到 428 时会自动走 <StepUpDialog> 确认密码并重发同一请求, 调用方无需处理。
export function revokeTrustedDevice(id: number, signal?: AbortSignal) {
    return apiRequest<{ revoked: number }>(`/api/v1/user/2fa/trusted-devices/${id}`, { method: 'DELETE', signal });
}

export function revokeAllTrustedDevices(signal?: AbortSignal) {
    return apiRequest<{ revoked: number }>('/api/v1/user/2fa/trusted-devices', { method: 'DELETE', signal });
}

// regenerateRecoveryCodes 用当前动态码(或一张未用的恢复码)换 10 张新恢复码;
// 旧码在服务端被整体替换, 调用者要把新码当作唯一一份凭据当场展示。
export function regenerateRecoveryCodes(code: string, signal?: AbortSignal) {
    return apiRequest<{ recovery_codes: string[] }>('/api/v1/user/2fa/recovery-codes/regenerate', {
        method: 'POST', body: { code }, signal,
    });
}
