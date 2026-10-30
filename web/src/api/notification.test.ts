import { describe, expect, it } from 'vitest';
import { notificationNavigation, notificationTypeKey, type NotificationType } from './notification';
import en from '@/locales/en.json';

describe('notificationNavigation', () => {
    it('工单通知落到工单页并带上关联工单号', () => {
        expect(notificationNavigation('ticket', 42)).toEqual({ page: 'tickets', focusTicketId: 42 });
    });

    it('工单通知缺关联号时只跳工单页', () => {
        const target = notificationNavigation('ticket');
        expect(target?.page).toBe('tickets');
        expect(target?.focusTicketId).toBeUndefined();
    });

    it('related_id 非法(0/负数)不带聚焦', () => {
        expect(notificationNavigation('ticket', 0)?.focusTicketId).toBeUndefined();
        expect(notificationNavigation('ticket', -3)?.focusTicketId).toBeUndefined();
    });

    it('其余通知类型暂不渲染跳转入口', () => {
        // 后端工单通知 link 恒为空串(见 op.NotifyTicket), 只有类型 + related_id 可用;
        // 其它类型没有稳定落地页, 一律 null, 不给用户点进去落空的入口。
        const others: NotificationType[] = ['system', 'balance_low', 'ratelimit_hit', 'channel_down', 'channel_review', 'withdrawal', 'recharge', 'dispute'];
        for (const type of others) {
            expect(notificationNavigation(type, 1)).toBeNull();
        }
    });
});

describe('notificationTypeKey', () => {
    it('清洗类型名再拼文案键', () => {
        // 与通知中心原有的清洗口径一致: 只保留小写字母与下划线, 再补 type_ 前缀。
        expect(notificationTypeKey('ticket')).toBe('type_ticket');
        expect(notificationTypeKey('ratelimit_hit')).toBe('type_ratelimit_hit');
        expect(notificationTypeKey('low!')).toBe('type_low'); // 标点被剥掉
        expect(notificationTypeKey('a_b')).toBe('type_a_b'); // 下划线保留
        expect(notificationTypeKey('A_low')).toBe('type__low'); // 大写被剥掉, 下划线保留
    });

    it('每种通知类型都有对应文案键', () => {
        // 缺键在 use-intl 里是直接抛错(整页白屏), 用测试把「加类型忘补文案」拦下来。
        const keys = Object.keys(en.notification);
        const types: NotificationType[] = ['system', 'balance_low', 'ratelimit_hit', 'channel_down', 'channel_review', 'withdrawal', 'recharge', 'dispute', 'ticket'];
        for (const type of types) {
            expect(keys).toContain(notificationTypeKey(type));
        }
    });

    it('工单跳转文案键三语齐全(键位一致性由 locales.test.ts 兜底)', () => {
        expect(en.notification.viewTicket).toBeTruthy();
    });
});
