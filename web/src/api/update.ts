import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';

export interface UpdateStatus {
    current_version: string;
    paused: boolean;
    check_enabled: boolean;
    check_interval_min: number;
    last_check_at: number;
    update_available: boolean;
    up_to_date: boolean;
    check_failed: boolean;
    check_error: string;
    platform: string;
    expected_asset: string;
    asset_missing: boolean;
    latest_version: string;
    latest_published_at: string;
    latest_body: string;
    release_url: string;
    asset_name: string;
    asset_size: number;
    asset_sha256: string;
    download_url: string;
}

export interface UpdateResult {
    from: string;
    to: string;
    asset: string;
    exec_path: string;
    backup_path: string;
}

export function useUpdateStatus(enabled = true) {
    return useQuery({
        queryKey: ['update', 'status'],
        queryFn: () => apiRequest<UpdateStatus>('/api/v1/update'),
        refetchInterval: 3600000,
        refetchOnMount: 'always',
        enabled,
    });
}

export function useCheckUpdate() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: () => apiRequest<UpdateStatus>('/api/v1/update/check', { method: 'POST' }),
        onSuccess: (data) => queryClient.setQueryData(['update', 'status'], data),
    });
}

export function useNowVersion() {
    return useQuery({
        queryKey: ['update', 'now-version'],
        queryFn: () => apiRequest<string>('/api/v1/update/now-version'),
        refetchInterval: 3600000,
        refetchOnMount: 'always',
    });
}

export function useUpdateCore(force = false) {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: () => apiRequest<UpdateResult>('/api/v1/update' + (force ? '?force=1' : ''), { method: 'POST' }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['update', 'status'] });
            queryClient.invalidateQueries({ queryKey: ['update', 'now-version'] });
        },
    });
}
