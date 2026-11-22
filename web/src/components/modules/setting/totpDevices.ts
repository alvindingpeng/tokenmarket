import type { TrustedDevice } from '@/api/totp';

// 信任设备清单的展示规则(纯逻辑, 组件文件只导出组件)。
//
// 这里刻意不用 toLocaleString: 它会跟着浏览器语言/时区变, 同一个时间戳在两个用户的截图里
// 长得不一样, 排查时对不上。设备清单是"用户自己核对有没有陌生机器", 需要的是稳定可比对的形状。

// formatDeviceTime 把后端下发的 RFC3339 时间戳格式化成 "YYYY-MM-DD HH:mm"(本地时间)。
// 解析失败返回空串而不是 "Invalid Date": 后者会被当成真实文案印在界面上。
export function formatDeviceTime(value: string | undefined | null): string {
    if (typeof value !== 'string' || value === '') return '';
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return '';
    const pad = (n: number) => String(n).padStart(2, '0');
    return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

// deviceLabel 把 UA 收成一行可辨认的标签。
// 原样展示会把设置页撑宽(UA 动辄 150 字符), 而截断到 64 字符仍足够认出"是我的 Chrome 还是陌生脚本"。
// 空 UA 回落到 "unknown": 留空会让整行只剩一个撤销按钮, 用户不知道自己在吊销什么。
export function deviceLabel(device: Pick<TrustedDevice, 'user_agent'>): string {
    const ua = typeof device.user_agent === 'string' ? device.user_agent.trim() : '';
    if (ua === '') return 'unknown';
    return ua.length > 64 ? ua.slice(0, 64) + '…' : ua;
}

// canRegenerate 判断"重新生成恢复码"这块要不要显示。
// 只有 2FA 已启用才有恢复码可言; 未启用时给按钮只会让人点了报错。
export function canRegenerate(enabled: boolean): boolean {
    return enabled;
}

// remainingRecoveryLabel 组装"剩余可用 N 个"里的数字; 负数/NaN 一律按 0 处理,
// 因为"剩余 -1 个"比"剩余 0 个"更让人以为界面坏了。数量由后端给定, 前端不自己数。
export function clampRecoveryCount(value: number | undefined | null): number {
    if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) return 0;
    return Math.floor(value);
}
