/**
 * API Key 的展示与提交载荷构造。
 *
 * 明文只在创建响应里出现一次, 其余场合都只有掩码片段(key_prefix + key_last4);
 * Key 一旦签发不可替换, 所以任何提交载荷都不带密钥材料。这两条由这里的纯函数保证,
 * 便于单测钉住, 也免得每个组件各自拼字符串时把明文或 Key 材料带出去。
 */

/** 掩码中间的填充符: 与历史展示样式一致。 */
export const API_KEY_MASK_FILLER = '********';

/** API Key 表单状态: 不含任何密钥材料 —— 明文由服务端生成, 旧 Key 不可替换。 */
export interface APIKeyFormState {
    name: string;
    enabled?: boolean;
    expire_at?: number;
    max_cost?: number;
    supported_models?: string[];
    routing_preference?: string;
    quota_requests?: number;
    quota_input_tokens?: number;
    quota_output_tokens?: number;
    quota_reset_hour?: number;
}

/** 创建载荷: 只送可编辑字段, 密钥值留给后端生成并一次性返回。 */
export interface APIKeyCreatePayload extends APIKeyFormState {
    name: string;
}

/** 更新载荷: 永远不含 api_key / key_hash —— 禁止更新替换原 Key。 */
export interface APIKeyUpdatePayload extends APIKeyCreatePayload {
    id: number;
}

/**
 * 掩码展示: prefix + '********' + last4。
 * 两段都缺时返回空串, 免得页面上出现一串毫无意义的星号。
 */
export function maskAPIKey(prefix?: string | null, last4?: string | null): string {
    const head = prefix ?? '';
    const tail = last4 ?? '';
    if (!head && !tail) return '';
    return `${head}${API_KEY_MASK_FILLER}${tail}`;
}

/**
 * 创建载荷。金额与期限清空后显式送 0(= 无限制/永不过期),
 * 否则「未提交即保留」的后端语义会把旧值留在原地。
 */
export function toAPIKeyCreatePayload(form: APIKeyFormState): APIKeyCreatePayload {
    return {
        name: form.name,
        enabled: form.enabled ?? true,
        expire_at: form.expire_at ?? 0,
        max_cost: form.max_cost ?? 0,
        supported_models: form.supported_models ?? [],
        routing_preference: form.routing_preference ?? 'inherit',
        quota_requests: form.quota_requests ?? 0,
        quota_input_tokens: form.quota_input_tokens ?? 0,
        quota_output_tokens: form.quota_output_tokens ?? 0,
        quota_reset_hour: form.quota_reset_hour ?? 0,
    };
}

/** 更新载荷: 同创建载荷, 多带主键; 密钥材料一概不送。 */
export function toAPIKeyUpdatePayload(id: number, form: APIKeyFormState): APIKeyUpdatePayload {
    return { id, ...toAPIKeyCreatePayload(form) };
}
