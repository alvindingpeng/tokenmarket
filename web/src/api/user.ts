import { useEffect } from 'react';
import { queryOptions, useMutation } from '@tanstack/react-query';
import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import { apiRequest, apiUnauthorizedEvent, clearStepUpToken, currentAuthGeneration, resetAuthSession, setAPIKey } from './client';
import type { UserTier } from '@/lib/user-tier';

/**
 * 用户登录请求
 */
export interface UserLoginRequest {
    username: string;
    password: string;
    totp_code?: string;
    expire: number; // 登录状态过期时间，正数为秒，-1 表示 30 天。
    /**
     * trust_device 只在本次登录真的验过动态码时生效: 后端把当前浏览标记为信任设备 30 天,
     * 期间这台机器的登录只验密码。
     *
     * 它免的是**第二因子**, 不是密码 —— 两者别混。后端也不会因为这里传 true 就跳过验码:
     * 没给 totp_code 的请求拿不到信任设备。
     */
    trust_device?: boolean;
}

/**
 * 用户角色：管理员、渠道商、普通用户。
 */
export type Role = 'admin' | 'reseller' | 'user';

/**
 * 用户视图（与后端 model.UserView 对齐）。
 */
export type UserView = {
    id: number;
    username: string;
    email: string;
    totp_enabled?: boolean;
    /**
     * totp_recovery_remaining 剩余未使用的恢复码数量(仅启用 2FA 时有值)。
     *
     * 只用于提示"快用完了, 该重新生成"; 恢复码本身(以及哈希)永远不会下发。
     * 缺失按 0 处理 —— 但不能据此断言"没有了": 旧后端不带这个字段时, 0 只表示"不知道"。
     */
    totp_recovery_remaining?: number;
    role: Role;
    status: 'active' | 'pending' | 'disabled';
    // tier 服务等级: 存量账号可能回空串(未设置), 后端按 free 兜底; 界面据此显示"未设置"。
    tier?: UserTier | '';
    balance: number;
    frozen: number;
    total_spent: number;
    total_revenue: number;
    total_recharged: number;
    created_at: string;
    // timezone 时区偏好(IANA 名, 如 Asia/Shanghai)。空串或字段缺省 = 跟随服务器时区,
    // 后端对从未设置过的老账号返回的就是这个值 —— 界面据此把第一项显示为"跟随服务器"。
    timezone?: string;
};

/**
 * 注册开关配置（登录页据其决定展示哪些注册入口）。
 */
export type RegisterConfig = {
    user_enabled: boolean;
    reseller_enabled: boolean;
    approval_required: boolean;
};

/**
 * 注册请求；渠道商注册可附申请理由，用户注册无理由字段。
 * 密码输入两遍（password/password_confirm），邮箱验证码（email_code）校验通过才能注册。
 */
export interface UserRegisterRequest {
    username: string;
    password: string;
    password_confirm: string;
    email: string;
    email_code: string;
    reason?: string;
}

/**
 * 邮箱验证码请求；purpose: register(注册) | reset(找回密码)。
 */
export interface EmailCodeRequest {
    email: string;
    purpose: 'register' | 'reset';
}

/**
 * 找回密码请求：邮箱验证码通过后重设密码，同样输入两遍。
 */
export interface ForgotPasswordRequest {
    email: string;
    email_code: string;
    password: string;
    password_confirm: string;
}

/**
 * 认证状态 Store
 */
interface AuthState {
    isAuthenticated: boolean;
    isLoading: boolean;
    isAPIKeyAuth: boolean;
    token: string | null;
    role: Role | null; // 当前登录用户角色，用于按角色裁剪导航与页面。
    userId: number; // 当前用户 id，钱包页查本人流水与管理层引用用。
    // timezone 当前账号的时区偏好(空串 = 跟随服务器)。账号在界面上选完立刻写这里,
    // 免得先乐观改本地再等下一次 checkAuth —— 设置项是受控的, 显示值必须只有一个来源。
    timezone: string;

    // Actions
    setAuth: (user: UserView) => void;
    setAPIKeyAuth: (apiKey: string) => void;
    checkAuth: () => Promise<void>;
    logout: () => void;
}

/**
 * 认证状态管理 Store（使用 zustand + persist）
 */
let authCheck = 0;

export const useAuthStore = create<AuthState>()(
    persist(
        (set, get) => ({
            isAuthenticated: false,
            isLoading: true,
            isAPIKeyAuth: false,
            token: null,
            role: null,
            userId: 0,
            timezone: '',

            setAuth: (user: UserView) => {
                setAPIKey(null);
                resetAuthSession();
                set({
                    isAuthenticated: true,
                    isAPIKeyAuth: false,
                    token: null,
                    role: user.role,
                    userId: user.id,
                    timezone: user.timezone ?? '',
                    isLoading: false
                });
            },

            setAPIKeyAuth: (apiKey: string) => {
                setAPIKey(apiKey);
                resetAuthSession();
                set({
                    isAuthenticated: true,
                    isAPIKeyAuth: true,
                    token: apiKey,
                    role: null,
                    userId: 0,
                    timezone: '',
                    isLoading: false
                });
            },

            checkAuth: async () => {
                const generation = currentAuthGeneration();
                const check = ++authCheck;
                const isCurrent = () => generation === currentAuthGeneration() && check === authCheck;
                const { token, isAPIKeyAuth } = get();
                setAPIKey(isAPIKeyAuth ? token : null);

                if (isAPIKeyAuth && !token) {
                    get().logout();
                    return;
                }

                try {
                    const endpoint = isAPIKeyAuth ? '/api/v1/apikey/login' : '/api/v1/user/status';
                    const profile = await apiRequest<UserView>(endpoint, { dispatchUnauthorized: false });
                    if (!isCurrent()) return;
                    if (get().userId !== (profile?.id ?? 0) || get().role !== (profile?.role ?? null)) {
                        resetAuthSession();
                    }
                    set({
                        isAuthenticated: true,
                        isLoading: false,
                        token: isAPIKeyAuth ? token : null,
                        role: profile?.role ?? null,
                        userId: profile?.id ?? 0,
                        // 每次 checkAuth 都以服务端为准。老账号(或后端尚未下发该字段时)拿到空串,
                        // 语义就是"跟随服务器", 与界面的第一项一致。
                        timezone: profile?.timezone ?? ''
                    });
                } catch {
                    if (isCurrent()) get().logout();
                }
            },

            logout: () => {
                setAPIKey(null);
                resetAuthSession();
                set({
                    isAuthenticated: false,
                    isAPIKeyAuth: false,
                    token: null,
                    role: null,
                    userId: 0,
                    timezone: '',
                    isLoading: false
                });
                if (typeof document !== 'undefined') {
                    const cookiePath = new URL('./', document.baseURI).pathname; // 当前应用所在目录对应的 Cookie 路径。
                    document.cookie = `auth=; Max-Age=0; Path=${cookiePath}; SameSite=Lax`;
                    // 反向代理重写 Cookie Path 时也可省略目录末尾的斜杠。
                    if (cookiePath !== '/') {
                        document.cookie = `auth=; Max-Age=0; Path=${cookiePath.slice(0, -1)}; SameSite=Lax`;
                    }
                }
            }
        }),
        {
            name: 'auth-storage',
            // timezone 有意**不**进 partialize: 它是账号侧事实, 每次 checkAuth 都会从服务端刷新,
            // 持久化只会留下过期值 —— 用户在另一台设备改完时区后, 这台设备会一直显示旧选择。
            // 只持久化恢复会话必须的三项(token/isAPIKeyAuth/role)。
            partialize: (state) => ({
                token: state.token,
                isAPIKeyAuth: state.isAPIKeyAuth,
                role: state.role,
            })
        }
    )
);

/**
 * 用户登录 Hook
 * 
 * @example
 * const login = useLogin();
 * login.mutate({ username: 'admin', password: '123456', expire: 86400 });
 * 
 * if (login.isPending) return <Loading />;
 * if (login.isError) return <Error message={login.error.message} />;
 */
export function loginUser(data: UserLoginRequest) {
    setAPIKey(null);
    return apiRequest<UserView>('/api/v1/user/login', {
        method: 'POST', body: data, dispatchUnauthorized: false,
    });
}

export function useLogin() {
    const { setAuth } = useAuthStore();

    return useMutation({
        mutationFn: loginUser,
        onSuccess: (user: UserView) => {
            setAuth(user);
        },
    });
}

/**
 * 注册配置查询定义（登录页与启动预取共用）。
 */
export const registerConfigQueryOptions = queryOptions({
    queryKey: ['user', 'register-config'],
    queryFn: () => apiRequest<RegisterConfig>('/api/v1/user/register-config'),
});

/**
 * 注册 Hook：kind 决定注册为普通用户还是渠道商，角色由接口固定，不随请求提交。
 */
export function useRegister(kind: 'user' | 'reseller') {
    return useMutation({
        mutationFn: (data: UserRegisterRequest) =>
            apiRequest<UserView>(kind === 'user' ? '/api/v1/user/register' : '/api/v1/user/register-reseller', {
                method: 'POST',
                body: data,
                dispatchUnauthorized: false,
            }),
    });
}

/**
 * 发送邮箱验证码 Hook：注册与找回密码共用，60 秒冷却由后端限流。
 */
export function useSendEmailCode() {
    return useMutation({
        mutationFn: (data: EmailCodeRequest) =>
            apiRequest<string>('/api/v1/user/email-code', {
                method: 'POST',
                body: data,
                dispatchUnauthorized: false,
            }),
    });
}

/**
 * 找回密码 Hook：邮箱验证码通过后重设密码，旧会话全部吊销。
 */
export function useForgotPassword() {
    return useMutation({
        mutationFn: (data: ForgotPasswordRequest) =>
            apiRequest<string>('/api/v1/user/forgot-password', {
                method: 'POST',
                body: data,
                dispatchUnauthorized: false,
            }),
    });
}

/**
 * 修改密码 Hook
 * 
 * @example
 * const changePassword = useChangePassword();
 * changePassword.mutate({ oldPassword: '123', newPassword: '456' });
 */
export function useChangePassword() {
    return useMutation({
        mutationFn: (data: { oldPassword: string; newPassword: string }) =>
            apiRequest<string>('/api/v1/user/change-password', {
                method: 'POST',
                body: {
                    old_password: data.oldPassword,
                    new_password: data.newPassword,
                },
            }),
        onSuccess: () => clearStepUpToken(), // 改密即换 token_version: 旧的二次验证凭证当场作废, 本地也不留。
    });
}

/**
 * 修改用户名 Hook
 * 
 * @example
 * const changeUsername = useChangeUsername();
 * changeUsername.mutate({ newUsername: 'newname' });
 */
export function useChangeUsername() {
    return useMutation({
        mutationFn: (data: { newUsername: string }) =>
            apiRequest<string>('/api/v1/user/change-username', {
                method: 'POST',
                body: { new_username: data.newUsername },
            }),
    });
}

/**
 * 时区偏好 Hook
 *
 * 改时区**不影响** token: 与改用户名(会吊销旧会话、迫使重登)不同, 这里就地更新 store,
 * 用户选完立刻看到结果, 不必重新登录。
 *
 * @example
 * const updateTimezone = useUpdateTimezone();
 * updateTimezone.mutate({ timezone: 'Asia/Shanghai' });
 */
export function useUpdateTimezone() {
    return useMutation({
        mutationFn: (data: { timezone: string }) =>
            apiRequest<string>('/api/v1/user/timezone', {
                method: 'POST',
                body: { timezone: data.timezone },
            }),
        onSuccess: (normalized, variables) => {
            // 就地在 store 上生效, 不重新登录、不等下一次 checkAuth: 否则界面会先回跳旧值再跳回来。
            //
            // 有服务端回显时以回显为准: 后端会把时区名归一(asia/shanghai -> Asia/Shanghai)再回传,
            // 直接存提交值会让界面显示 "asia/shanghai" 而库里是 "Asia/Shanghai",
            // 到下一次 checkAuth 又"莫名其妙地变了个写法"。回显缺失(旧后端)时退回提交值。
            useAuthStore.setState({
                timezone: typeof normalized === 'string' ? normalized : variables.timezone,
            });
        },
    });
}

/**
 * 认证状态和方法 Hook
 * 
 * @example
 * const auth = useAuth();
 * 
 * if (auth.isAuthenticated) {
 *   // 已登录
 * }
 * 
 * auth.logout(); // 登出
 */
export function useAuth() {
    const store = useAuthStore();
    const { checkAuth, isLoading } = store;

    // 只在首次挂载时检查认证状态
    useEffect(() => {
        const handleUnauthorized = () => useAuthStore.getState().logout();
        window.addEventListener(apiUnauthorizedEvent, handleUnauthorized);
        if (isLoading) {
            void checkAuth();
        }
        return () => window.removeEventListener(apiUnauthorizedEvent, handleUnauthorized);
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []); // 有意只在挂载时执行一次

    return {
        isAuthenticated: store.isAuthenticated,
        isAPIKeyAuth: store.isAPIKeyAuth,
        isLoading: store.isLoading,
        role: store.role,
        logout: store.logout,
    };
}
