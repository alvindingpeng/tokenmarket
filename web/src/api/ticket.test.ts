import { describe, expect, it } from 'vitest';
import {
    assignableAdmins,
    ticketCanClose,
    ticketCanReply,
    ticketCanReopen,
    ticketListQuery,
    ticketOpeningContent,
    ticketStatusOptions,
    TICKET_CATEGORIES,
    TICKET_CONTENT_MAX,
    TICKET_PRIORITIES,
    TICKET_STATUSES,
    TICKET_TITLE_MAX,
} from './ticket';
import type { UserView } from './user';

// 枚举顺序就是状态机与后端常量顺序(见 internal/model/ticket.go),
// 改动意味着后端口径变了 —— 这里按数量与顺序固化, 防止前端悄悄漂移。
describe('ticket enums', () => {
    it('状态机四态齐全且有序', () => {
        expect(TICKET_STATUSES).toHaveLength(4);
        expect(TICKET_STATUSES).toEqual(['open', 'in_progress', 'resolved', 'closed']);
    });

    it('优先级与分类与后端一致', () => {
        expect(TICKET_PRIORITIES).toEqual(['low', 'normal', 'high', 'urgent']);
        expect(TICKET_CATEGORIES).toEqual(['billing', 'channel', 'account', 'other']);
    });
});

describe('ticketStatusOptions', () => {
    it('可选项 = 当前状态 + 流转表允许的目标', () => {
        expect(ticketStatusOptions('open')).toHaveLength(3);
        expect(ticketStatusOptions('open')).toEqual(['open', 'in_progress', 'closed']);
        expect(ticketStatusOptions('closed')).toEqual(['closed', 'open']);
        expect(ticketStatusOptions('resolved')).toEqual(['resolved', 'in_progress', 'closed']);
    });
});

describe('输入长度上限', () => {
    it('与后端 model 常量一致', () => {
        // op.ticketTitle/ticketContent 按 RuneCountInString 卡 255/10000, 前端表单共用这两个常量。
        expect(TICKET_TITLE_MAX).toBe(255);
        expect(TICKET_CONTENT_MAX).toBe(10000);
    });
});

describe('合法操作口径', () => {
    it('关单只在未关闭时可点', () => {
        expect(ticketCanClose('open')).toBe(true);
        expect(ticketCanClose('in_progress')).toBe(true);
        expect(ticketCanClose('resolved')).toBe(true);
        expect(ticketCanClose('closed')).toBe(false);
    });

    it('重开按钮只挂在已关闭工单上', () => {
        expect(ticketCanReopen('closed')).toBe(true);
        for (const status of ['open', 'in_progress', 'resolved'] as const) {
            expect(ticketCanReopen(status)).toBe(false);
        }
    });

    it('关闭后一律禁回复, 与角色无关', () => {
        // 后端 TicketReply 对任何角色都回 ErrTicketClosed(见 op.TicketReply), 没有管理员例外。
        expect(ticketCanReply('closed')).toBe(false);
        for (const status of ['open', 'in_progress', 'resolved'] as const) {
            expect(ticketCanReply(status)).toBe(true);
        }
    });
});

describe('ticketOpeningContent', () => {
    const ticket = { user_id: 7, content: 'I was charged twice.' };

    it('消息列表不含首正文时单独渲染(后端建单不落消息行)', () => {
        expect(ticketOpeningContent(ticket, [])).toBe('I was charged twice.');
        // 管理员碰巧发了同样文字也不算首正文。
        expect(
            ticketOpeningContent(ticket, [{ user_id: 1, content: 'I was charged twice.', is_admin: true }]),
        ).toBe('I was charged twice.');
    });

    it('消息列表已含首正文(工单主人发的同文消息)则不重复渲染', () => {
        expect(
            ticketOpeningContent(ticket, [{ user_id: 7, content: 'I was charged twice.', is_admin: false }]),
        ).toBeNull();
        // 别的用户发同样文字不算首正文。
        expect(
            ticketOpeningContent(ticket, [{ user_id: 8, content: 'I was charged twice.', is_admin: false }]),
        ).toBe('I was charged twice.');
    });
});

describe('ticketListQuery', () => {
    it('无筛选时只带分页参数', () => {
        expect(ticketListQuery({}, 1, 20)).toBe('page=1&page_size=20');
    });

    it('筛选项逐个落到查询串', () => {
        const query = ticketListQuery({ status: 'open', priority: 'high', category: 'billing', assigned_to: 7 }, 2, 20);
        expect(query).toContain('status=open');
        expect(query).toContain('priority=high');
        expect(query).toContain('category=billing');
        expect(query).toContain('assigned_to=7');
        expect(query).toContain('page=2');
    });

    it('空值筛选不落进查询串', () => {
        const query = ticketListQuery({ status: '', priority: '', category: '', assigned_to: 0 }, 1, 20);
        expect(query).toBe('page=1&page_size=20');
    });

    it('页码下限为 1', () => {
        expect(ticketListQuery({}, 0, 20)).toBe('page=1&page_size=20');
    });
});

describe('assignableAdmins', () => {
    const user = (id: number, role: UserView['role'], status: UserView['status']): UserView =>
        ({ id, username: 'u' + id, role, status }) as UserView;

    it('只留启用中的管理员', () => {
        const admins = assignableAdmins([
            user(1, 'admin', 'active'),
            user(2, 'admin', 'disabled'),
            user(3, 'admin', 'pending'),
            user(4, 'user', 'active'),
            user(5, 'reseller', 'active'),
        ]);
        expect(admins).toHaveLength(1);
        expect(admins[0].id).toBe(1);
    });

    it('空列表返回空数组', () => {
        expect(assignableAdmins([])).toEqual([]);
    });
});
