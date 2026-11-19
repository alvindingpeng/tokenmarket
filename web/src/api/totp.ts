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
