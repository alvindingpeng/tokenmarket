import { queryOptions, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';
import type { Role, UserView } from './user';

/**
 * 用户管理请求：管理员创建与更新用户共用；更新时密码留空即不改。
 * balance 为可选绝对余额，未提交表示不调整，提交 0 表示清零。
 */
export interface UserManageRequest {
    id?: number;
    username: string;
    password?: string;
    role: Role;
    status: 'active' | 'pending' | 'disabled';
    balance?: number;
}

// userManageListQueryOptions 供用户管理页与启动预取共享的用户列表定义（管理员专用接口）。
export const userManageListQueryOptions = queryOptions({
    queryKey: ['users', 'list'],
    queryFn: () => apiRequest<UserView[]>('/api/v1/user/manage/list'),
});

/**
 * useUserList 用户列表查询（仅管理员）。
 */
export function useUserList() {
    return useQuery(userManageListQueryOptions);
}

/**
 * useCreateUser 创建用户；角色与状态由管理员指定。
 */
export function useCreateUser() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (data: UserManageRequest) =>
            apiRequest<UserView>('/api/v1/user/manage/create', {
                method: 'POST',
                body: data,
            }),
        onSuccess: () => {
            void queryClient.invalidateQueries({ queryKey: ['users', 'list'] });
        },
    });
}

/**
 * useUpdateUser 更新用户角色/状态/用户名/密码。
 */
export function useUpdateUser() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (data: UserManageRequest) =>
            apiRequest<string>('/api/v1/user/manage/update', {
                method: 'POST',
                body: data,
            }),
        onSuccess: () => {
            void queryClient.invalidateQueries({ queryKey: ['users', 'list'] });
        },
    });
}

/**
 * useResetPassword 管理员重置指定用户密码，同时吊销其全部登录会话。
 */
export function useResetPassword() {
    return useMutation({
        mutationFn: (data: { id: number; password: string }) =>
            apiRequest<string>('/api/v1/user/manage/reset-password', {
                method: 'POST',
                body: data,
            }),
    });
}

/**
 * useDeleteUser 删除用户（不可删除自己或最后一个管理员由后端校验）。
 */
export function useDeleteUser() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (id: number) =>
            apiRequest<string>(`/api/v1/user/manage/delete/${id}`, {
                method: 'DELETE',
            }),
        onSuccess: () => {
            void queryClient.invalidateQueries({ queryKey: ['users', 'list'] });
        },
    });
}
