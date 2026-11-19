import { queryOptions, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';

// Tenant types aligned with backend model.Tenant
export type TenantStatus = 'active' | 'disabled' | 'pending';

export interface Tenant {
    id: number;
    name: string;
    code: string;
    status: TenantStatus;
    created_at: string;
    updated_at: string;
}

export interface TenantQuota {
    id: number;
    tenant_id: number;
    requests_limit: number;
    input_tokens_limit: number;
    output_tokens_limit: number;
    period: 'daily' | 'weekly' | 'monthly';
    reset_day: number;
    created_at: string;
    updated_at: string;
}

export interface TenantWallet {
    id: number;
    tenant_id: number;
    balance: number;
    frozen: number;
    total_recharged: number;
    total_spent: number;
    total_revenue: number;
    total_withdrawn: number;
    withdraw_frozen: number;
    created_at: string;
    updated_at: string;
}

// TenantLedger 租户资金流水(充值/调账), 供客户对账与平台审计。
export interface TenantLedger {
    id: number;
    tenant_id: number;
    kind: 'recharge' | 'adjust' | 'withdraw' | string;
    amount: number;
    note: string;
    actor_id: number;
    actor_name: string;
    created_at: string;
}

export interface TenantLedgerPage {
    items: TenantLedger[];
    total: number;
}

export interface TenantPricingPolicy {
    id: number;
    tenant_id: number;
    channel_id: number | null;
    model_name: string | null;
    markup_ratio: number;
    currency: string;
    is_active: boolean;
    priority: number;
    created_at: string;
    updated_at: string;
}

// TenantQuotaUsage 配额上限与当前周期用量并排; 百分比由后端算好(0 表示不限, 前端据此隐藏进度条)。
export interface TenantQuotaUsage {
    quota: TenantQuota;
    requests: number;
    input_tokens: number;
    output_tokens: number;
    reset_at: string;
    requests_pct: number;
    input_tokens_pct: number;
    output_tokens_pct: number;
}

// TenantMember 租户成员视图, 对齐后端 op.TenantMemberView。
export interface TenantMember {
    user_id: number;
    username: string;
    tenant_id: number;
    role: 'owner' | 'admin' | 'member' | string; // 租户内角色
    user_role: string; // 平台角色, 供对照
    status: string;
    created_at: string;
}

export interface CreateTenantRequest {
    name: string;
    code: string;
    status?: TenantStatus;
}

export interface UpdateTenantRequest {
    name?: string;
    code?: string;
    status?: TenantStatus;
}

export interface CreateTenantQuotaRequest {
    requests_limit: number;
    input_tokens_limit: number;
    output_tokens_limit: number;
    period: 'daily' | 'weekly' | 'monthly';
    reset_day: number;
}

export interface CreateTenantPricingPolicyRequest {
    // null 表示"全渠道/全模型生效"; 显式 null 与省略不同, 后端按 null 建一条通配策略。
    channel_id?: number | null;
    model_name?: string | null;
    markup_ratio: number;
    currency?: string;
    is_active?: boolean;
    priority?: number;
}

// API functions
export async function fetchTenants(): Promise<Tenant[]> {
    return apiRequest<Tenant[]>('/api/v1/admin/tenant/list');
}

export async function fetchTenant(id: number): Promise<Tenant> {
    return apiRequest<Tenant>(`/api/v1/admin/tenant/${id}`);
}

export async function createTenant(data: CreateTenantRequest): Promise<Tenant> {
    return apiRequest<Tenant>('/api/v1/admin/tenant/create', {
        method: 'POST',
        body: JSON.stringify(data),
    });
}

export async function updateTenant(id: number, data: UpdateTenantRequest): Promise<Tenant> {
    return apiRequest<Tenant>(`/api/v1/admin/tenant/${id}`, {
        method: 'PUT',
        body: JSON.stringify(data),
    });
}

export async function deleteTenant(id: number): Promise<void> {
    await apiRequest<null>(`/api/v1/admin/tenant/${id}`, {
        method: 'DELETE',
    });
}

export async function fetchTenantQuota(tenantId: number): Promise<TenantQuota> {
    return apiRequest<TenantQuota>(`/api/v1/admin/tenant/${tenantId}/quota`);
}

export async function updateTenantQuota(tenantId: number, data: CreateTenantQuotaRequest): Promise<TenantQuota> {
    return apiRequest<TenantQuota>(`/api/v1/admin/tenant/${tenantId}/quota`, {
        method: 'PUT',
        body: JSON.stringify(data),
    });
}

// fetchTenantQuotaUsage 取配额上限与当前周期用量, 供详情面板直接算进度条。
export async function fetchTenantQuotaUsage(tenantId: number): Promise<TenantQuotaUsage> {
    return apiRequest<TenantQuotaUsage>(`/api/v1/admin/tenant/${tenantId}/quota/usage`);
}

export async function fetchTenantWallet(tenantId: number): Promise<TenantWallet> {
    return apiRequest<TenantWallet>(`/api/v1/admin/tenant/${tenantId}/wallet`);
}

export async function fetchTenantPricingPolicies(tenantId: number): Promise<TenantPricingPolicy[]> {
    return apiRequest<TenantPricingPolicy[]>(`/api/v1/admin/tenant/${tenantId}/pricing`);
}

export async function createTenantPricingPolicy(tenantId: number, data: CreateTenantPricingPolicyRequest): Promise<TenantPricingPolicy> {
    return apiRequest<TenantPricingPolicy>(`/api/v1/admin/tenant/${tenantId}/pricing`, {
        method: 'POST',
        body: JSON.stringify(data),
    });
}

export async function updateTenantPricingPolicy(tenantId: number, policyId: number, data: Partial<CreateTenantPricingPolicyRequest>): Promise<TenantPricingPolicy> {
    return apiRequest<TenantPricingPolicy>(`/api/v1/admin/tenant/${tenantId}/pricing/${policyId}`, {
        method: 'PUT',
        body: JSON.stringify(data),
    });
}

export async function deleteTenantPricingPolicy(tenantId: number, policyId: number): Promise<void> {
    await apiRequest<null>(`/api/v1/admin/tenant/${tenantId}/pricing/${policyId}`, {
        method: 'DELETE',
    });
}

// React Query hooks
export function useTenants() {
    return queryOptions({
        queryKey: ['tenants'],
        queryFn: fetchTenants,
    });
}

export function useTenant(id: number) {
    return queryOptions({
        queryKey: ['tenant', id],
        queryFn: () => fetchTenant(id),
        enabled: id > 0,
    });
}

export function useTenantQuota(tenantId: number) {
    return queryOptions({
        queryKey: ['tenantQuota', tenantId],
        queryFn: () => fetchTenantQuota(tenantId),
        enabled: tenantId > 0,
    });
}

export function useTenantQuotaUsage(tenantId: number) {
    return queryOptions({
        queryKey: ['tenantQuotaUsage', tenantId],
        queryFn: () => fetchTenantQuotaUsage(tenantId),
        enabled: tenantId > 0,
    });
}

// 资金操作直接改动客户可用额度: 后端要求 step-up 二次验证, 前端只需把错误抛给调用方展示。
export async function rechargeTenantWallet(tenantId: number, amount: number, note: string): Promise<TenantWallet> {
    return apiRequest<TenantWallet>(`/api/v1/admin/tenant/${tenantId}/wallet/recharge`, {
        method: 'POST',
        body: JSON.stringify({ amount, note }),
    });
}

export async function adjustTenantWallet(tenantId: number, amount: number, note: string): Promise<TenantWallet> {
    return apiRequest<TenantWallet>(`/api/v1/admin/tenant/${tenantId}/wallet/adjust`, {
        method: 'POST',
        body: JSON.stringify({ amount, note }),
    });
}

export async function fetchTenantLedger(tenantId: number, limit = 20, offset = 0): Promise<TenantLedgerPage> {
    return apiRequest<TenantLedgerPage>(`/api/v1/admin/tenant/${tenantId}/wallet/ledger?limit=${limit}&offset=${offset}`);
}

export function useTenantLedger(tenantId: number) {
    return queryOptions({
        queryKey: ['tenantLedger', tenantId],
        queryFn: () => fetchTenantLedger(tenantId),
        enabled: tenantId > 0,
    });
}

export function useTenantWallet(tenantId: number) {
    return queryOptions({
        queryKey: ['tenantWallet', tenantId],
        queryFn: () => fetchTenantWallet(tenantId),
        enabled: tenantId > 0,
    });
}

// 租户成员端点: 后端随后上线, 这里先按可缺省处理, 缺失时界面优雅提示而不是报红。
export async function fetchTenantMembers(tenantId: number): Promise<TenantMember[]> {
    return apiRequest<TenantMember[]>(`/api/v1/admin/tenant/${tenantId}/members`);
}

export async function addTenantMember(tenantId: number, userId: number, role: string): Promise<void> {
    await apiRequest<null>(`/api/v1/admin/tenant/${tenantId}/members`, {
        method: 'POST',
        body: JSON.stringify({ user_id: userId, role }),
    });
}

export async function removeTenantMember(tenantId: number, userId: number): Promise<void> {
    await apiRequest<null>(`/api/v1/admin/tenant/${tenantId}/members/${userId}`, {
        method: 'DELETE',
    });
}

export function useTenantMembers(tenantId: number) {
    return queryOptions({
        queryKey: ['tenantMembers', tenantId],
        queryFn: () => fetchTenantMembers(tenantId),
        enabled: tenantId > 0,
        retry: false,
    });
}

export function useTenantPricingPolicies(tenantId: number) {
    return queryOptions({
        queryKey: ['tenantPricingPolicies', tenantId],
        queryFn: () => fetchTenantPricingPolicies(tenantId),
        enabled: tenantId > 0,
    });
}

export function useCreateTenant() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: createTenant,
        onSuccess: () => {
            qc.invalidateQueries({ queryKey: ['tenants'] });
        },
    });
}

export function useUpdateTenant() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: ({ id, data }: { id: number; data: UpdateTenantRequest }) => updateTenant(id, data),
        onSuccess: (_, { id }) => {
            qc.invalidateQueries({ queryKey: ['tenants'] });
            qc.invalidateQueries({ queryKey: ['tenant', id] });
        },
    });
}

export function useDeleteTenant() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: deleteTenant,
        onSuccess: () => {
            qc.invalidateQueries({ queryKey: ['tenants'] });
        },
    });
}

export function useUpdateTenantQuota() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: ({ tenantId, data }: { tenantId: number; data: CreateTenantQuotaRequest }) => updateTenantQuota(tenantId, data),
        onSuccess: (_, { tenantId }) => {
            qc.invalidateQueries({ queryKey: ['tenantQuota', tenantId] });
        },
    });
}

export function useCreateTenantPricingPolicy() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: ({ tenantId, data }: { tenantId: number; data: CreateTenantPricingPolicyRequest }) => createTenantPricingPolicy(tenantId, data),
        onSuccess: (_, { tenantId }) => {
            qc.invalidateQueries({ queryKey: ['tenantPricingPolicies', tenantId] });
        },
    });
}

export function useUpdateTenantPricingPolicy() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: ({ tenantId, policyId, data }: { tenantId: number; policyId: number; data: Partial<CreateTenantPricingPolicyRequest> }) => updateTenantPricingPolicy(tenantId, policyId, data),
        onSuccess: (_, { tenantId }) => {
            qc.invalidateQueries({ queryKey: ['tenantPricingPolicies', tenantId] });
        },
    });
}

export function useDeleteTenantPricingPolicy() {
    const qc = useQueryClient();
    return useMutation({
        mutationFn: ({ tenantId, policyId }: { tenantId: number; policyId: number }) => deleteTenantPricingPolicy(tenantId, policyId),
        onSuccess: (_, { tenantId }) => {
            qc.invalidateQueries({ queryKey: ['tenantPricingPolicies', tenantId] });
        },
    });
}

// User tenant API functions (non-admin)
export async function fetchUserTenants(): Promise<Tenant[]> {
    return apiRequest<Tenant[]>('/api/v1/user/tenant/list');
}

export async function fetchUserDefaultTenant(): Promise<Tenant | null> {
    try {
        return await apiRequest<Tenant>('/api/v1/user/tenant/default');
    } catch {
        return null;
    }
}

export function useUserTenants() {
    return queryOptions({
        queryKey: ['userTenants'],
        queryFn: fetchUserTenants,
    });
}

export function useUserDefaultTenant() {
    return queryOptions({
        queryKey: ['userDefaultTenant'],
        queryFn: fetchUserDefaultTenant,
    });
}