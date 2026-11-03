import { describe, expect, it } from 'vitest';
import type { UserView } from '@/api/user';
import { buildCreateUserRequest, buildUpdateUserRequest, passwordProblem } from './payload';

// 表单"哪些字段该发"的规则逐条钉死: 多发的字段会污染审计, 少发的字段会让用户以为改成功。

const user: UserView = {
    id: 3,
    username: 'alice',
    email: 'alice@example.com',
    role: 'user',
    status: 'active',
    tier: 'basic',
    balance: 12.5,
    frozen: 0,
    total_spent: 0,
    total_revenue: 0,
    total_recharged: 0,
    created_at: '2026-01-01T00:00:00Z',
};

describe('buildCreateUserRequest', () => {
    it('创建总是带上 tier, 用户名去空白', () => {
        const request = buildCreateUserRequest({
            username: '  bob  ', email: ' b@x.com ', password: 'pw', role: 'user', status: 'active', tier: 'pro',
        });
        expect(request).toMatchObject({ username: 'bob', email: 'b@x.com', password: 'pw', role: 'user', status: 'active', tier: 'pro' });
    });
});

describe('buildUpdateUserRequest', () => {
    it('全部字段都未变时只发 id/username/role/status, 不带 tier/balance/password/email', () => {
        const request = buildUpdateUserRequest({
            user, username: 'alice', email: '', password: '', role: 'user', status: 'active', balance: '12.5', tier: 'basic',
        });
        expect(request).toEqual({ id: 3, username: 'alice', role: 'user', status: 'active' });
        expect(request).not.toHaveProperty('tier');
        expect(request).not.toHaveProperty('balance');
        expect(request).not.toHaveProperty('password');
        expect(request).not.toHaveProperty('email');
    });

    it('等级真的变了才下发 tier', () => {
        const request = buildUpdateUserRequest({
            user, username: 'alice', email: '', password: '', role: 'user', status: 'active', balance: '12.5', tier: 'enterprise',
        });
        expect(request.tier).toBe('enterprise');
    });

    it('tier 空串 = 保持原等级(存量账号未设置时不下发)', () => {
        const unset: UserView = { ...user, tier: '' };
        const request = buildUpdateUserRequest({
            user: unset, username: 'alice', email: '', password: '', role: 'user', status: 'active', balance: '12.5', tier: '',
        });
        expect(request).not.toHaveProperty('tier');
    });

    it('密码/邮箱只有在非空时才下发(留空=保留原值)', () => {
        const request = buildUpdateUserRequest({
            user, username: 'alice', email: 'new@x.com', password: 'newpw', role: 'user', status: 'active', balance: '12.5', tier: 'basic',
        });
        expect(request.password).toBe('newpw');
        expect(request.email).toBe('new@x.com');
    });

    it('余额与原值一致不发送; 不一致且合法才发送', () => {
        const same = buildUpdateUserRequest({
            user, username: 'alice', email: '', password: '', role: 'user', status: 'active', balance: '12.50', tier: 'basic',
        });
        expect(same).not.toHaveProperty('balance');

        const changed = buildUpdateUserRequest({
            user, username: 'alice', email: '', password: '', role: 'user', status: 'active', balance: '99', tier: 'basic',
        });
        expect(changed.balance).toBe(99);
    });

    it('非法或负余额不下发', () => {
        for (const balance of ['', 'abc', '-5']) {
            const request = buildUpdateUserRequest({
                user, username: 'alice', email: '', password: '', role: 'user', status: 'active', balance, tier: 'basic',
            });
            expect(request).not.toHaveProperty('balance');
        }
    });
});

describe('passwordProblem', () => {
    it('空串表示本次不设密码, 不算问题(编辑时留空=不改)', () => {
        expect(passwordProblem('')).toBeNull();
    });

    it('短于后端下限要被拦下', () => {
        // 后端 op/user.go 对创建/编辑/重置三处都强制 8 位; 前端此前只判非空,
        // 于是填 6 位点提交必定被拒, 而且只看到英文原文。
        expect(passwordProblem('1234567')).toBe('too_short');
    });

    it('刚好 8 位及格(闭区间, 与后端 len(p) < 8 一致)', () => {
        expect(passwordProblem('12345678')).toBeNull();
        expect(passwordProblem('a-much-longer-password')).toBeNull();
    });
});
