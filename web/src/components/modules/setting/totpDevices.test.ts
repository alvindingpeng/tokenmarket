import { describe, expect, it } from 'vitest';
import { canRegenerate, clampRecoveryCount, deviceLabel, formatDeviceTime } from './totpDevices';

// 信任设备清单的展示规则。这些函数决定"用户看到的到底是哪台机器", 错了不会报错, 只会误导。

describe('formatDeviceTime', () => {
    it('格式化成本地 YYYY-MM-DD HH:mm', () => {
        const iso = '2026-11-22T10:30:00+08:00';
        const date = new Date(iso);
        const pad = (n: number) => String(n).padStart(2, '0');
        const expected = `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
        expect(formatDeviceTime(iso)).toBe(expected);
    });

    it('非法/缺失值返回空串, 而不是 "Invalid Date"', () => {
        // "Invalid Date" 会被当成真实文案印在设置页上, 用户会来问"我的设备时间为什么是 Invalid"。
        for (const bad of ['', 'not-a-date', undefined, null]) {
            expect(formatDeviceTime(bad)).toBe('');
        }
    });
});

describe('deviceLabel', () => {
    it('空 UA 回落成 unknown 而不是留空', () => {
        // 留空会让整行只剩一个"吊销"按钮, 用户不知道自己在吊销什么。
        expect(deviceLabel({ user_agent: '' })).toBe('unknown');
        expect(deviceLabel({ user_agent: '   ' })).toBe('unknown');
    });

    it('超长 UA 截断并加省略号', () => {
        const ua = 'M'.repeat(200);
        const label = deviceLabel({ user_agent: ua });
        expect(label.endsWith('…')).toBe(true);
        expect(label.length).toBeLessThan(ua.length);
    });

    it('正常长度 UA 原样保留', () => {
        expect(deviceLabel({ user_agent: 'Mozilla/5.0 (Windows NT 10.0)' })).toBe('Mozilla/5.0 (Windows NT 10.0)');
    });
});

describe('canRegenerate', () => {
    it('只有 2FA 已启用才显示重新生成', () => {
        // 未启用时按钮只会在点击后报"2FA 未启用" —— 那是一个不该出现的入口。
        expect(canRegenerate(true)).toBe(true);
        expect(canRegenerate(false)).toBe(false);
    });
});

describe('clampRecoveryCount', () => {
    it('正常值取整', () => {
        expect(clampRecoveryCount(7)).toBe(7);
        expect(clampRecoveryCount(7.9)).toBe(7);
    });

    it('负数/NaN/缺失一律 0', () => {
        // "剩余 -1 个"比"剩余 0 个"更像界面坏了。
        for (const bad of [-1, Number.NaN, Infinity, undefined, null]) {
            expect(clampRecoveryCount(bad)).toBe(0);
        }
    });
});
