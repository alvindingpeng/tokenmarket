import { describe, expect, it } from 'vitest';
import type { QueryClient } from '@tanstack/react-query';
import { renderSSR } from '@/test/ssr';
import { SettingKey } from '@/api/setting';
import { SettingAudit } from './Audit';
import { SettingTierPolicies } from './TierPolicies';

// 两张新设置卡片的 mock SSR 冒烟:
//   - 审计归档卡片要把留存天数/开关回显出来, 且归档文件列表能渲染;
//   - 等级配额卡片要把四个等级 × 三个维度铺成可编辑输入框, 并在设置值损坏时给出提示
//     (而不是静默显示默认值 —— 那会让管理员以为旧配额还在生效)。

function seedSettings(client: QueryClient, values: Record<string, string>) {
    client.setQueryData(['settings', 'list'], Object.entries(values).map(([key, value]) => ({ key, value })));
}

describe('SettingAudit 审计归档卡片(mock SSR)', () => {
    it('回显留存天数与开关, 并渲染归档文件', () => {
        const html = renderSSR(
            <SettingAudit />,
            (client) => {
                seedSettings(client, {
                    [SettingKey.AuditRetentionDays]: '30',
                    [SettingKey.AuditArchiveEnabled]: 'false',
                    [SettingKey.ArchiveRetentionDays]: '60',
                });
                client.setQueryData(['ops', 'audit-archives'], {
                    items: [{ name: 'audit-20260101.ndjson', size: 2048, mod_time: '2026-01-01T00:00:00Z' }],
                    retention_days: 30,
                    enabled: false,
                });
            },
        );
        expect(html).toContain('Audit Archive');
        expect(html).toContain('Audit retention (days)');
        expect(html).toContain('value="30"'); // 留存天数回显
        expect(html).toContain('audit-20260101.ndjson'); // 归档文件
        expect(html).toContain('2.0 KB'); // 大小按人读格式
        expect(html).toContain('Archive now'); // 立即归档入口
        // 归档文件留存与数据库留存是两个不同的键: 前者删早了补不回来, 必须能单独配置。
        expect(html).toContain('Archive file retention (days)');
        expect(html).toContain('value="60"');
    });

    it('无归档文件时渲染空态', () => {
        const html = renderSSR(
            <SettingAudit />,
            (client) => {
                seedSettings(client, { [SettingKey.AuditRetentionDays]: '180' });
                client.setQueryData(['ops', 'audit-archives'], { items: [], retention_days: 180, enabled: true });
            },
        );
        expect(html).toContain('No archive files yet');
    });
});

describe('SettingTierPolicies 等级配额卡片(mock SSR)', () => {
    it('四个等级 × 三个维度铺成输入框, 并回显已保存数值', () => {
        const html = renderSSR(
            <SettingTierPolicies />,
            (client) => seedSettings(client, {
                [SettingKey.UserTierPolicies]: JSON.stringify({
                    free: { rpm: 11, tpm: 22, concurrent: 3 },
                    basic: { rpm: 44, tpm: 55, concurrent: 6 },
                    pro: { rpm: 77, tpm: 88, concurrent: 9 },
                    enterprise: { rpm: 0, tpm: 0, concurrent: 0 },
                }),
            }),
        );
        expect(html).toContain('User Tier Quotas');
        expect(html).toContain('Free');
        expect(html).toContain('Enterprise');
        // 每档三个维度: 4 档 × 3 = 12 个可编辑输入框。
        expect(html.match(/data-slot="input"/g)).toHaveLength(12);
        expect(html).toContain('value="11"');
        expect(html).toContain('value="88"');
    });

    it('设置值损坏时给出明确错误, 而不是静默显示默认值', () => {
        const html = renderSSR(
            <SettingTierPolicies />,
            (client) => seedSettings(client, { [SettingKey.UserTierPolicies]: '{broken' }),
        );
        expect(html).toContain('not valid JSON');
        // 旧值坏掉时管理员仍应能用这张表单覆盖它: 保存按钮不能被永久禁用。
        const saveButton = html.slice(html.lastIndexOf('<button'), html.indexOf('</button>', html.lastIndexOf('<button')));
        expect(saveButton).not.toContain('disabled=""');
    });

    it('从未配置(空串)时按默认配额展示且不报错', () => {
        const html = renderSSR(<SettingTierPolicies />, (client) => seedSettings(client, {}));
        expect(html).toContain('User Tier Quotas');
        expect(html).not.toContain('not valid JSON');
        expect(html.match(/data-slot="input"/g)).toHaveLength(12);
    });
});