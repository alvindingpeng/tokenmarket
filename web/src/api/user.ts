import { useEffect } from 'react';
import { queryOptions, useMutation } from '@tanstack/react-query';
import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import { apiRequest, apiUnauthorizedEvent, setAPIKey } from './client';

/**
 * 用户登录请求
 */
interface UserLoginRequest {
    username: string;
    password: string;
    expire: number; // 登录状态过期时间，正数为秒，-1 表示 30 天。
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
    role: Role;
    status: 'active' | 'pending' | 'disabled';
    balance: number;
    frozen: number;
    total_spent: number;
    total_revenue: number;
    total_recharged: number;
    created_at: string;
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
 */
export interface UserRegisterRequest {
    username: string;
    password: string;
    reason?: string;
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
    balance: number; // 当前可用余额，计费页展示用。
    frozen: number; // 当前冻结额（预扣中），计费页展示用。
    username: string; // 当前用户名，顶栏与账户页展示用。

    // Actions
    setAuth: (user: UserView) => void;
    setAPIKeyAuth: (apiKey: string) => void;
    checkAuth: () => Promise<void>;
    logout: () => void;
}

/**
 * 认证状态管理 Store（使用 zustand + persist）
 */
export const useAuthStore = create<AuthState>()(
    persist(
        (set, get) => ({
            isAuthenticated: false,
            isLoading: true,
            isAPIKeyAuth: false,
            token: null,
            role: null,
            balance: 0,
            frozen: 0,
            username: '',

            setAuth: (user: UserView) => {
                setAPIKey(null);
                set({
                    isAuthenticated: true,
                    isAPIKeyAuth: false,
                    token: null,
                    role: user.role,
                    balance: user.balance,
                    frozen: user.frozen,
                    username: user.username,
                    isLoading: false
                });
            },

            setAPIKeyAuth: (apiKey: string) => {
                setAPIKey(apiKey);
                set({
                    isAuthenticated: true,
                    isAPIKeyAuth: true,
                    token: apiKey,
                    role: null,
                    isLoading: false
                });
            },

            checkAuth: async () => {
                const { token, isAPIKeyAuth } = get();
                setAPIKey(isAPIKeyAuth ? token : null);

                if (isAPIKeyAuth && !token) {
                    set({ isAuthenticated: false, isLoading: false });
                    return;
                }

                try {
                    const endpoint = isAPIKeyAuth ? '/api/v1/apikey/login' : '/api/v1/user/status';
                    const profile = await apiRequest<UserView>(endpoint, { dispatchUnauthorized: false });
                    set({
                        isAuthenticated: true,
                        isLoading: false,
                        token: isAPIKeyAuth ? token : null,
                        role: profile?.role ?? null,
                        balance: profile?.balance ?? 0,
                        frozen: profile?.frozen ?? 0,
                        username: profile?.username ?? ''
                    });
                } catch {
                    get().logout();
                }
            },

            logout: () => {
                setAPIKey(null);
                set({
                    isAuthenticated: false,
                    isAPIKeyAuth: false,
                    token: null,
                    role: null,
                    balance: 0,
                    frozen: 0,
                    username: '',
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
export function useLogin() {
    const { setAuth } = useAuthStore();

    return useMutation({
        mutationFn: async (data: UserLoginRequest) => {
            setAPIKey(null);
            return apiRequest<UserView>('/api/v1/user/login', {
                method: 'POST',
                body: data,
                dispatchUnauthorized: false,
            });
        },
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
        balance: store.balance,
        frozen: store.frozen,
        username: store.username,
        logout: store.logout,
    };
}
