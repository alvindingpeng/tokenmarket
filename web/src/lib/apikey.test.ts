import { describe, expect, it } from 'vitest';
import {
    API_KEY_MASK_FILLER,
    maskAPIKey,
    toAPIKeyCreatePayload,
    toAPIKeyUpdatePayload,
} from './apikey';

describe('maskAPIKey', () => {
    it('拼出 prefix + 星号 + last4 的掩码', () => {
        expect(maskAPIKey('sk-switchyard-', 'abcd')).toBe(`sk-switchyard-${API_KEY_MASK_FILLER}abcd`);
    });

    it('片段缺失时不出空洞掩码', () => {
        expect(maskAPIKey('', '')).toBe('');
        expect(maskAPIKey(null, undefined)).toBe('');
        expect(maskAPIKey('only-prefix', '')).toContain('only-prefix');
    });

    it('掩码不含明文中段: 前缀与尾段之外只剩填充符', () => {
        const masked = maskAPIKey('sk-12345678', 'wxyz');
        expect(masked.replace(API_KEY_MASK_FILLER, '')).toBe('sk-12345678wxyz');
    });
});

describe('APIKey 提交载荷', () => {
    const form = {
        name: 'my key',
        supported_models: ['gpt'],
        quota_requests: 5,
    };

    it('创建载荷只带可编辑字段, 不带任何密钥材料', () => {
        const payload = toAPIKeyCreatePayload(form);
        expect(payload.name).toBe('my key');
        expect(payload).not.toHaveProperty('api_key');
        expect(payload).not.toHaveProperty('key_hash');
        expect(payload).not.toHaveProperty('key_prefix');
        expect(payload).not.toHaveProperty('key_last4');
    });

    it('更新载荷带主键且同样不带密钥材料(禁止更新替换原 Key)', () => {
        const payload = toAPIKeyUpdatePayload(7, form);
        expect(payload.id).toBe(7);
        for (const forbidden of ['api_key', 'key_hash', 'key_prefix', 'key_last4']) {
            expect(payload).not.toHaveProperty(forbidden);
        }
    });

    it('清空的金额与期限显式送 0, 否则后端会保留旧值', () => {
        const payload = toAPIKeyCreatePayload({ name: 'x' });
        expect(payload.max_cost).toBe(0);
        expect(payload.expire_at).toBe(0);
    });

    it('已有值原样回传: 编辑不能清掉权限与配额', () => {
        const payload = toAPIKeyUpdatePayload(1, {
            name: 'x',
            expire_at: 1700000000,
            max_cost: 3.5,
            supported_models: ['gpt'],
            quota_requests: 5,
            quota_input_tokens: 6,
            quota_output_tokens: 7,
            quota_reset_hour: 8,
        });
        expect(payload.expire_at).toBe(1700000000);
        expect(payload.max_cost).toBe(3.5);
        expect(payload.supported_models).toEqual(['gpt']);
        expect(payload.quota_requests).toBe(5);
        expect(payload.quota_input_tokens).toBe(6);
        expect(payload.quota_output_tokens).toBe(7);
        expect(payload.quota_reset_hour).toBe(8);
    });
});
