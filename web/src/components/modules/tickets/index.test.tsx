import { describe, expect, it } from 'vitest';
import type { QueryClient } from '@tanstack/react-query';
import { ticketListQueryOptions, TICKET_PAGE_SIZE, type TicketDetailView, type TicketView } from '@/api/ticket';
import { renderSSR } from '@/test/ssr';
import { Tickets } from './index';
import { TicketDetail } from './TicketDetail';

// 页面组件的 mock SSR 冒烟: 不起服务器, 用预置 query 缓存把组件渲染到「有数据」分支,
// 断言关键标记(标题/状态/可访问标签/回复框)真的出现在 HTML 里。

const row: TicketView = {
    id: 42,
    user_id: 7,
    username: 'alice',
    title: 'Billing question',
    content: 'I was charged twice.',
    status: 'open',
    priority: 'high',
    category: 'billing',
    message_count: 3,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-02T00:00:00Z',
};

const detail: TicketDetailView = {
    ...row,
    messages: [
        { id: 1, ticket_id: 42, user_id: 7, username: 'alice', content: 'I was charged twice.', is_admin: false, created_at: '2026-01-01T00:00:00Z' },
        { id: 2, ticket_id: 42, user_id: 1, username: 'root', content: 'We are checking.', is_admin: true, created_at: '2026-01-01T01:00:00Z' },
    ],
};

const seedList = (client: QueryClient) => {
    // 缓存数据按查询定义的类型只放 {items,total}; 响应里的 page/page_size 组件不消费。
    client.setQueryData(ticketListQueryOptions({ status: '', priority: '', category: '', assigned_to: 0 }, 1, TICKET_PAGE_SIZE, false).queryKey, {
        items: [row],
        total: 1,
    });
};

describe('Tickets 页面(mock SSR)', () => {
    it('列表渲染行数据、筛选与创建入口', () => {
        const html = renderSSR(<Tickets />, seedList);
        expect(html).toContain('Billing question'); // 列表行标题
        expect(html).toContain('New ticket'); // 创建入口(用户端)
        expect(html).toContain('aria-label="Filter by status"'); // 筛选可访问标签
        expect(html).toContain('aria-label="View #42"'); // 行操作可访问标签
        expect(html).toContain('Open'); // 状态徽标文案(不靠颜色)
        expect(html).not.toContain('Loading...'); // 有数据就不再停在加载态
    });

    it('空列表渲染空态提示', () => {
        const html = renderSSR(<Tickets />, (client) => {
            client.setQueryData(ticketListQueryOptions({ status: '', priority: '', category: '', assigned_to: 0 }, 1, TICKET_PAGE_SIZE, false).queryKey, {
                items: [],
                total: 0,
            });
        });
        expect(html).toContain('No tickets');
    });
});

describe('TicketDetail 页面(mock SSR)', () => {
    const seedDetail = (client: QueryClient) => client.setQueryData(['ticket', 'detail', 'user', 42], detail);

    it('对话渲染消息、回复框与关闭入口', () => {
        const html = renderSSR(<TicketDetail ticketId={42} admin={false} onBack={() => {}} />, seedDetail);
        expect(html).toContain('We are checking.'); // 管理员回复内容
        expect(html).toContain('Admin'); // 管理员回复徽标
        expect(html).toContain('id="ticket-reply"'); // 回复框
        expect(html).toContain('maxLength="10000"'); // 回复长度上限与后端一致(React 19 SSR 原样输出属性名, HTML 属性名大小写不敏感)
        expect(html).toContain('aria-label="Back to list"'); // 标题行返回入口
        expect(html).toContain('Close ticket'); // 未关闭工单的关闭入口
        expect(html).toContain('Text only in this release'); // 本期纯文本提示
    });

    it('消息列表不含首正文时, 正文单独渲染在对话开头', () => {
        // 后端建单不落消息行, messages 只有后续回复; 首正文必须从 ticket.content 单独渲染。
        const html = renderSSR(<TicketDetail ticketId={42} admin={false} onBack={() => {}} />, (client) => {
            client.setQueryData(['ticket', 'detail', 'user', 42], {
                ...detail,
                messages: detail.messages.filter((message) => message.is_admin),
            });
        });
        expect(html.match(/I was charged twice\./g)).toHaveLength(1); // 恰好一次: 首正文卡片
        expect(html).toContain('We are checking.');
    });

    it('消息列表已含首正文时不重复渲染', () => {
        // 种子里 message[0] 就是工单主人的首正文, 正文只能出现一次, 不能卡片+消息各来一遍。
        const html = renderSSR(<TicketDetail ticketId={42} admin={false} onBack={() => {}} />, seedDetail);
        expect(html.match(/I was charged twice\./g)).toHaveLength(1);
    });

    it('已关闭工单禁回复并给重开入口', () => {
        const html = renderSSR(<TicketDetail ticketId={42} admin={false} onBack={() => {}} />, (client) => {
            client.setQueryData(['ticket', 'detail', 'user', 42], { ...detail, status: 'closed' });
        });
        expect(html).toContain('Reopen ticket');
        expect(html).toContain('This ticket is closed');
        expect(html).toMatch(/id="ticket-reply"[^>]*disabled/); // 回复框对关闭工单停用
    });

    it('已关闭工单对管理员同样禁回复(后端无角色例外)', () => {
        const html = renderSSR(<TicketDetail ticketId={42} admin onBack={() => {}} />, (client) => {
            client.setQueryData(['ticket', 'detail', 'admin', 42], { ...detail, status: 'closed' });
        });
        expect(html).toContain('This ticket is closed');
        expect(html).toMatch(/id="ticket-reply"[^>]*disabled/);
    });

    it('加载中也有返回列表入口', () => {
        // 不预置缓存: SSR 不触发请求, 组件停在加载分支(无数据)。
        // 只给转圈不给返回, 请求卡住时用户会被困死在详情页。
        const html = renderSSR(<TicketDetail ticketId={42} admin={false} onBack={() => {}} />);
        expect(html).toContain('aria-label="Back to list"');
        expect(html).toContain('Loading...');
    });

    it('无数据时同时给重试和返回列表', () => {
        // ticketId=null 让查询停用(enabled=false): 既不是 loading 也没有数据,
        // 组件落到 isError || !ticket 分支 —— 与真实请求报错后是同一条 UI。
        // 只有「重试」而没有「返回列表」时, 请求一直失败就等于把用户困死在这一页。
        // null 让 useTicketDetail 的 enabled=false: 既不取数也没有数据, 等价于"请求没回来"。
        const html = renderSSR(<TicketDetail ticketId={null as unknown as number} admin={false} onBack={() => {}} />);
        expect(html).toContain('aria-label="Back to list"');
        expect(html).toContain('Retry');
    });
});
