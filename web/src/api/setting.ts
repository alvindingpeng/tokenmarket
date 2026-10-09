import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ApiError, apiRequest } from './client';

/**
 * Setting 数据
 */
export interface Setting {
    key: string;
    value: string;
}

export const SettingKey = {
    ProxyURL: 'proxy_url',
    StatsSaveInterval: 'stats_save_interval',
    ModelInfoUpdateInterval: 'model_info_update_interval',
    CORSAllowOrigins: 'cors_allow_origins',
    ModelFilter: 'model_filter',
    MarkupRatio: 'markup_ratio', // 渠道商供货价上浮比例, 用户价 = 供货价 × (1 + ratio)。
    RegisterUserEnabled: 'register_user_enabled',
    RegisterResellerEnabled: 'register_reseller_enabled',
    RegisterApprovalRequired: 'register_approval_required',
    MinBalance: 'min_balance', // 余额下限, 低于该值拒绝新请求。
    BalanceReserveOutputCap: 'balance_reserve_output_cap', // 预扣估算用的单请求输出 token 上限。
    BalanceReserveImages: 'balance_reserve_images', // 生图预扣保守张数(无 n 时按此冻结), 默认 4。
    ScorePriceWeight: 'score_price_weight', // 综合评分系统默认: 价格权重, 分组内填 0(未自定义)时生效。
    ScoreLatencyWeight: 'score_latency_weight', // 综合评分系统默认: 延迟权重。
    ScoreSuccessWeight: 'score_success_weight', // 综合评分系统默认: 成功率权重。
    // 系统信息配置: 站点身份信息与公告, 仅管理员可改, 公开端只在登录前读取。
    SiteName: 'site_name', // 站点名称, 留空回退默认标题。
    SiteDescription: 'site_description', // 站点描述, 展示在登录页与关于处。
    SiteContact: 'site_contact', // 联系方式。
    SiteAnnouncement: 'site_announcement', // 全局公告正文。
    SiteAnnouncementEnabled: 'site_announcement_enabled', // 公告开关。
    MaintenanceMode: 'maintenance_mode', // 维护模式开关。
    MaintenanceNotice: 'maintenance_notice', // 维护提示文案。
    // 第二批运营可靠性: 日志生命周期 / 自动备份 / 渠道健康 / 告警, 仅管理员可改。
    LogRetentionDays: 'log_retention_days', // 调用日志留存天数, 默认 7。
    LogArchiveEnabled: 'log_archive_enabled', // 清理前先归档, 默认 true。
    LogStoreBody: 'log_store_body', // 是否保存请求/响应正文, 默认 true。
    LogMaskFields: 'log_mask_fields', // 额外脱敏字段(逗号分隔 JSON key)。
    AutoBackupEnabled: 'auto_backup_enabled', // 自动备份开关, 默认 true。
    AutoBackupKeep: 'auto_backup_keep', // 备份保留份数, 默认 7。
    AutoBackupInterval: 'auto_backup_interval', // 备份间隔(小时), 默认 24。
    BackupEncrypt: 'backup_encrypt', // 备份加密开关, 默认 false; 口令在 config.json backup.passphrase。
    BackupRemote: 'backup_remote', // 备份异地推送开关, 默认 false; 地址凭据在 config.json backup.remote_*。
    HealthCheckEnabled: 'health_check_enabled', // 渠道健康探测开关, 默认 false。
    HealthCheckInterval: 'health_check_interval', // 探测间隔(分钟), 默认 30。
    HealthLatencyMS: 'health_latency_ms', // 延迟告警阈值(毫秒), 0 关闭, 默认 1000。
    AlertWebhookURL: 'alert_webhook_url', // 告警 Webhook 地址, 留空仅站内通知。
    AlertDedupMinutes: 'alert_dedup_minutes', // 告警去重窗口(分钟), 0 表示不去重。
    AlertFailStreak: 'alert_fail_streak', // 连续失败多少次判宕机(抖动抑制)。
    MetricsAuth: 'metrics_auth', // 探针鉴权: off(默认开放) | bearer(需令牌)。
} as const;

/**
 * 获取 Setting 列表 Hook
 * 
 * @example
 * const { data: settings, isLoading, error } = useSettingList();
 * 
 * if (isLoading) return <Loading />;
 * if (error) return <Error message={error.message} />;
 * 
 * settings?.forEach(setting => console.log(setting.key, setting.value));
 */
export function useSettingList() {
    return useQuery({
        queryKey: ['settings', 'list'],
        queryFn: () => apiRequest<Setting[]>('/api/v1/setting/list'),
        refetchInterval: 30000,
        refetchOnMount: 'always',
    });
}

/**
 * 设置 Setting Hook
 * 
 * @example
 * const setSetting = useSetSetting();
 * 
 * setSetting.mutate({
 *   key: 'theme',
 *   value: 'dark',
 * });
 */
export function useSetSetting() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (data: Setting) =>
            apiRequest<Setting>('/api/v1/setting/set', { method: 'POST', body: data }),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['settings', 'list'] });
            // 站点信息与注册开关同样由这个端点写入, 而消费方(登录页 / 浏览器标题 / 公告与
            // 维护横幅)各自缓存自己的查询键。后端无从知道刚改的键会被谁读, 所以前端一律广播
            // 失效: 否则管理员保存完「系统信息配置」, 要等页面刷新才看得到效果。
            queryClient.invalidateQueries({ queryKey: ['site', 'config'] });
            queryClient.invalidateQueries({ queryKey: ['user', 'register-config'] });
        },
    });
}

/**
 * 数据库导入/导出
 */
interface DBImportResult {
    rows_affected: Record<string, number>;
}

function parseFilename(contentDisposition: string | null): string | null {
    if (!contentDisposition) return null;
    // e.g. attachment; filename="octopus-export-20250101120000.json"
    const match = contentDisposition.match(/filename="([^"]+)"/i);
    return match?.[1] ?? null;
}

function exportFallbackFilename() {
    const d = new Date();
    const pad = (n: number) => String(n).padStart(2, '0');
    const ts = `${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}`;
    return `octopus-export-${ts}.json`;
}

async function downloadBlob(blob: Blob, filename: string) {
    const url = URL.createObjectURL(blob);
    try {
        const a = document.createElement('a');
        a.href = url;
        a.download = filename;
        document.body.appendChild(a);
        a.click();
        a.remove();
    } finally {
        URL.revokeObjectURL(url);
    }
}

/**
 * 导出数据库（下载 JSON 文件）
 */
export function useExportDB() {
    return useMutation({
        mutationFn: async () => {
            const res = await fetch('./api/v1/setting/export', {
                method: 'GET',
                credentials: 'include',
            });

            if (!res.ok) {
                const data = await res.json().catch(() => null) as { message?: string } | null;
                throw new ApiError(res.status, data?.message || `Request failed: ${res.status}`);
            }

            const blob = await res.blob();
            const filename = parseFilename(res.headers.get('content-disposition')) || exportFallbackFilename();
            await downloadBlob(blob, filename);
            return { filename };
        },
    });
}

/**
 * 导入数据库（上传 JSON 文件）
 */
export function useImportDB() {
    return useMutation({
        mutationFn: async (file: File) => {
            const form = new FormData();
            form.append('file', file);

            const res = await fetch('./api/v1/setting/import', {
                method: 'POST',
                body: form,
                credentials: 'include',
            });

            const contentType = res.headers.get('content-type') || '';
            const isJson = contentType.includes('application/json');
            const data = isJson
                ? await res.json() as { message?: string; data?: DBImportResult }
                : await res.text();

            if (!res.ok) {
                const message = typeof data === 'string' ? data : data.message;
                throw new ApiError(res.status, message || `Request failed: ${res.status}`);
            }

            // 支持后端标准 ApiResponse：{code,message,data:{...}}
            return typeof data === 'string' ? data as unknown as DBImportResult : data.data as DBImportResult;
        },
    });
}
