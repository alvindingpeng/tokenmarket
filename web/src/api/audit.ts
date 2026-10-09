import { useQuery } from '@tanstack/react-query';
import { apiRequest } from './client';

/**
 * AuditLog 一条管理员操作审计记录。
 */
export type AuditLog = {
    id: number;
    user_id: number;
    username: string;
    action: string;
    target: string;
    detail: string;
    created_at: string;
};

/**
 * AuditLogListParams 审计记录查询参数; 未填写的维度表示不限。
 */
export type AuditLogListParams = {
    limit?: number;
    offset?: number;
    /** keyword 模糊匹配操作者、目标与明细。 */
    keyword?: string;
    /** action 动作标识前缀, 如 user. */
    action?: string;
    /** operator 操作者用户名模糊匹配。 */
    operator?: string;
    /** from 起始时间(含), RFC3339。 */
    from?: string;
    /** to 结束时间(含), RFC3339。 */
    to?: string;
};

/**
 * useAuditLogs 按筛选条件分页拉取审计记录(倒序), 管理员专用。
 *
 * @example
 * const { data } = useAuditLogs({ limit: 20, offset: 0, action: 'user' });
 */
export function useAuditLogs(params: AuditLogListParams) {
    const { limit = 20, offset = 0, keyword, action, operator, from, to } = params;
    const query = new URLSearchParams({ limit: String(limit), offset: String(offset) });
    if (keyword) query.set('keyword', keyword);
    if (action) query.set('action', action);
    if (operator) query.set('operator', operator);
    if (from) query.set('from', from);
    if (to) query.set('to', to);
    const qs = query.toString();
    return useQuery({
        queryKey: ['audit', 'list', qs],
        queryFn: () => apiRequest<{ items: AuditLog[]; total: number }>('/api/v1/audit/list?' + qs),
        refetchInterval: 30000,
        placeholderData: (previous) => previous,
    });
}

/**
 * useAuditActions 拉取已有记录中出现过的动作标识, 供筛选下拉展示。
 */
export function useAuditActions() {
    return useQuery({
        queryKey: ['audit', 'actions'],
        queryFn: () => apiRequest<string[]>('/api/v1/audit/actions'),
        staleTime: 60000,
    });
}

/**
 * auditExportUrl 构造审计记录 CSV 下载地址(带同一套筛选条件, 单次上限 5000 条)。
 * 与列表共享 auditFilter 的序列化语义: 导出看到的集合 == 当前筛选命中的集合。
 */
export function auditExportUrl(filter: AuditLogListParams): string {
    const { limit, offset, ...rest } = filter;
    void limit; void offset; // 导出固定取全量(上限内), 不随分页走。
    const query = new URLSearchParams();
    if (rest.keyword) query.set('keyword', rest.keyword);
    if (rest.action) query.set('action', rest.action);
    if (rest.operator) query.set('operator', rest.operator);
    if (rest.from) query.set('from', rest.from);
    if (rest.to) query.set('to', rest.to);
    const qs = query.toString();
    return './api/v1/audit/export' + (qs ? '?' + qs : '');
}
