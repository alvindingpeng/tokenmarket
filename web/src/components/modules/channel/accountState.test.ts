import { describe, expect, it } from 'vitest';
import { ACCOUNT_EXPIRING_MS, accountStateOf, remainingOf } from './accountState';
import type { ChannelKey } from '@/api/channel';

// 账号四态的判据测试。
// 这一段是"账号还能不能用"的唯一根据, 错了不会有任何报错 —— 只会让标着"正常"的账号
// 其实已经转发不出去。所以每个分支都要钉住。

const NOW = Date.parse('2026-11-14T00:00:00Z');

function key(overrides: Partial<ChannelKey>): ChannelKey {
    return { name: 'k', key: '{}', enabled: true, account_kind: 'oauth', ...overrides };
}

describe('accountStateOf', () => {
    it('到期时间在远期, 且未被熔断 -> 正常', () => {
        expect(accountStateOf(key({ account_platform: 'codex', token_expires_at: '2027-01-01T00:00:00Z' }), NOW)).toBe('healthy');
    });

    it('剩余有效期进入阈值内 -> 将到期', () => {
        // 比阈值少一分钟, 稳稳落在"将到期"。
        const at = new Date(NOW + ACCOUNT_EXPIRING_MS - 60_000).toISOString();
        expect(accountStateOf(key({ token_expires_at: at }), NOW)).toBe('expiring');
    });

    it('恰好等于阈值 -> 将到期(边界含在内)', () => {
        const at = new Date(NOW + ACCOUNT_EXPIRING_MS).toISOString();
        expect(accountStateOf(key({ token_expires_at: at }), NOW)).toBe('expiring');
    });

    it('阈值外一分钟 -> 正常', () => {
        const at = new Date(NOW + ACCOUNT_EXPIRING_MS + 60_000).toISOString();
        expect(accountStateOf(key({ token_expires_at: at }), NOW)).toBe('healthy');
    });

    it('到期时间已过 -> 已失效', () => {
        expect(accountStateOf(key({ token_expires_at: '2026-11-13T00:00:00Z' }), NOW)).toBe('invalid');
    });

    it('cookie 类一律是需人工, 绝不显示成正常', () => {
        // 即使到期时间在远期: cookie 没有 refresh_token, 到期后无法自动恢复。
        expect(accountStateOf(key({ account_kind: 'cookie', account_platform: 'gemini', token_expires_at: '2027-01-01T00:00:00Z' }), NOW)).toBe('manual');
    });

    it('被熔断但到期时间还在未来 -> 已失效', () => {
        // 这是最容易被误判成"正常"的一种: 时间看着没问题, 但此刻转发出去就是失败。
        // 判时间的顺序必须排在熔断之后。
        const until = new Date(NOW + 60 * 60 * 1000).toISOString();
        expect(accountStateOf(key({ token_expires_at: '2027-01-01T00:00:00Z', disabled_until: until }), NOW)).toBe('invalid');
    });

    it('熔断已过期 -> 回到按时间判断', () => {
        const until = new Date(NOW - 60 * 60 * 1000).toISOString();
        expect(accountStateOf(key({ token_expires_at: '2027-01-01T00:00:00Z', disabled_until: until }), NOW)).toBe('healthy');
    });

    it('熔断时间无法解析 -> 按仍在熔断处理(宁可误报失效)', () => {
        expect(accountStateOf(key({ token_expires_at: '2027-01-01T00:00:00Z', disabled_until: 'not-a-date' }), NOW)).toBe('invalid');
    });

    it('oauth 但没有到期时间 -> 正常(Kiro 的 JSON 本就不带)', () => {
        expect(accountStateOf(key({ account_platform: 'kiro' }), NOW)).toBe('healthy');
    });

    it('非账号凭据 -> 需人工(调用方本应按 account_kind 分支)', () => {
        expect(accountStateOf({ name: 'k', key: 'sk-x', enabled: true }, NOW)).toBe('manual');
    });
});

describe('remainingOf', () => {
    it('不到一小时报分钟', () => {
        expect(remainingOf(new Date(NOW + 30 * 60_000).toISOString(), NOW)).toEqual({ minutes: 30 });
    });

    it('不到两天报小时', () => {
        expect(remainingOf(new Date(NOW + 5 * 60 * 60_000).toISOString(), NOW)).toEqual({ hours: 5 });
    });

    it('超过两天报天(精确到小时没有意义)', () => {
        expect(remainingOf(new Date(NOW + 10 * 24 * 60 * 60_000).toISOString(), NOW)).toEqual({ days: 10 });
    });

    it('已过期返回 null', () => {
        expect(remainingOf('2026-11-13T00:00:00Z', NOW)).toBeNull();
    });

    it('空值与坏值返回 null', () => {
        expect(remainingOf(undefined, NOW)).toBeNull();
        expect(remainingOf('bad', NOW)).toBeNull();
    });
});
