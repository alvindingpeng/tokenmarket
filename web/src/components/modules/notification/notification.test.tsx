import { describe, expect, it } from 'vitest';
import type { QueryClient } from '@tanstack/react-query';
import { notificationListQueryOptions, type NotificationListResponse } from '@/api/notification';
import { renderSSR } from '@/test/ssr';
import { NotificationCenter } from './NotificationCenter';

// 通知中心 mock SSR: 工单通知必须带「跳工单」入口(后端 link 恒为空串, 只能按类型落地),
// 其余类型没有稳定落地页, 不渲染入口。

const payload: NotificationListResponse = {
    items: [
        {
            id: 1,
            user_id: 7,
            type: 'ticket',
            title: 'Ticket #42 has a new reply',
            content: 'We are checking.',
            related_id: 42,
            status: 'unread',
            created_at: '2026-01-01T00:00:00Z',
        },
        {
            id: 2,
            user_id: 7,
            type: 'system',
            title: 'Maintenance tonight',
            content: 'Service restart at 02:00.',
            status: 'read',
            created_at: '2026-01-01T00:00:00Z',
        },
    ],
    total: 2,
    page: 1,
    page_size: 20,
};

describe('NotificationCenter(mock SSR)', () => {
    it('只有工单通知渲染跳转入口', () => {
        const html = renderSSR(<NotificationCenter />, (client: QueryClient) => {
            client.setQueryData(notificationListQueryOptions({ page: 1, page_size: 20 }).queryKey, payload);
        });
        expect(html.match(/View ticket/g)).toHaveLength(1); // 跳转入口数量 = 工单通知数
        expect(html).toContain('aria-label="View ticket: Ticket #42 has a new reply"'); // 可访问标签指向具体工单
        expect(html).toContain('Ticket reply'); // 类型徽标文案(不靠颜色)
    });
});
