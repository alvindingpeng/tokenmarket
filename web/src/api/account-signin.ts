import { apiRequest } from './client';
import type { AccountImportResult, AccountPlatform } from './channel';

// 网页 OAuth 登录的三个接口。
//
// 为什么不复用「粘贴 JSON 导入」: 那条路要求用户先在自己机器上跑 CLI、再找到凭据文件、
// 再把内容贴过来。对服务器部署的用户这中间任何一步都可能卡住, 而登录只需要他点一下授权页。

/** 一次登录的会话。 */
export type SignInBegin = {
    platform: AccountPlatform;
    authorize_url: string; // 要在浏览器里打开的授权页
    state: string; // 贴回来时用来绑定这次会话
    user_code_hint: string; // 告诉用户贴什么, 不同平台贴的东西不一样
    expires_in: number; // 会话有效期(秒), 过期要重新发起
};

/** 平台能力: 哪些平台能网页登录, 以及是否已配置好。 */
export type SignInPlatform = {
    platform: AccountPlatform;
    available: boolean;
    // 不可用时的原因(通常是部署没配 client); 可用时为空。
    reason?: string;
    auth_style?: 'code' | 'redirect';
    user_code_hint?: string;
    // 登录用的是谁的 OAuth 客户端: 'claude-code' | 'codex-cli' | 'self-registered'。
    // 授权页上会显示成该 CLI 在申请权限, 用户有权在授权前知道这一点。
    client_owner?: string;
};

export type SignInCompletePayload = {
    platform: AccountPlatform;
    state: string;
    pasted: string; // 用户贴回来的 code 或回调地址
    channel_id: number;
    name: string;
    enabled?: boolean;
    priority?: number;
};

export type SignInCompleteResult = {
    account: AccountImportResult;
    refreshed_now: boolean;
    warning?: string;
};

/** 列出支持网页登录的平台。 */
export function accountSignInPlatforms() {
    return apiRequest<SignInPlatform[]>('/api/v1/channel/account/signin/platforms');
}

/** 为某个平台生成授权 URL。 */
export function accountSignInBegin(platform: AccountPlatform) {
    return apiRequest<SignInBegin>('/api/v1/channel/account/signin/begin', {
        method: 'POST',
        body: { platform },
    });
}

/**
 * 用贴回来的内容完成登录, 成功即落库成渠道凭据。
 *
 * 超时放宽的原因与导入一致: 后端拿到 code 后要真的去令牌端点换一次, 再立刻刷一次。
 */
export function accountSignInComplete(payload: SignInCompletePayload) {
    return apiRequest<SignInCompleteResult>('/api/v1/channel/account/signin/complete', {
        method: 'POST',
        body: payload,
        timeoutMs: 90_000,
    });
}
