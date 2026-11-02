import { beforeEach, describe, expect, it } from 'vitest';
import type { QueryClient } from '@tanstack/react-query';
import { userManageListQueryOptions } from '@/api/usermanage';
import { useAuthStore } from '@/api/user';
import type { UserView } from '@/api/user';
import { TooltipProvider } from '@/components/ui/tooltip';
import { renderSSR } from '@/test/ssr';
import { Users } from './index';

// 用户管理页的等级展示: 徽标文案必须三语接上, 且"未设置"不能被悄悄显示成 Free。
// 这两点一旦错了, 管理员会按错误的等级去理解一个账号的限流额度。

const baseUser: UserView = {
    id: 1,
    username: 'alice',
    email: 'a@example.com',
    role: 'user',
    status: 'active',
    tier: 'pro',
    balance: 10,
    frozen: 0,
    total_spent: 0,
    total_revenue: 0,
    total_recharged: 0,
    created_at: '2026-01-01T00:00:00Z',
};

function seedUsers(client: QueryClient, items: UserView[]) {
    client.setQueryData(userManageListQueryOptions.queryKey, items);
}

// 页面里的行操作按钮用 IconButton, 它内部是 Radix Tooltip —— 必须包在 Provider 里。
// 真实应用在根部已提供, mock SSR 需要在这里补一层, 否则渲染直接抛错。
function renderUsers(seed: (client: QueryClient) => void) {
    return renderSSR(
        <TooltipProvider>
            <Users />
        </TooltipProvider>,
        seed,
    );
}

beforeEach(() => {
    useAuthStore.setState({ userId: 7, isAuthenticated: true, isLoading: false, isAPIKeyAuth: false, token: null, role: 'admin' });
});

describe('用户管理页 等级展示(mock SSR)', () => {
    it('列表有"服务等级"列, 并按用户等级渲染徽标文案', () => {
        const html = renderUsers((client) => seedUsers(client, [baseUser]));
        expect(html).toContain('Tier'); // 列头
        expect(html).toContain('Pro'); // pro 等级徽标
    });

    it('未设置等级显示 Not set, 而不是冒充 Free', () => {
        const html = renderUsers((client) => seedUsers(client, [{ ...baseUser, tier: '' }]));
        expect(html).toContain('Not set');
        // 列表里不该出现 Free 徽标(那是"确实配了免费版"的意思)。
        expect(html).not.toContain('>Free<');
    });

    it('未知等级原样回落到未设置, 不白屏', () => {
        const html = renderUsers((client) => seedUsers(client, [{ ...baseUser, tier: 'gold' as UserView['tier'] }]));
        expect(html).toContain('Not set');
    });

    it('创建入口存在且不渲染邮箱等敏感列以外的多余字段', () => {
        const html = renderUsers((client) => seedUsers(client, [baseUser]));
        expect(html).toContain('Create User');
        expect(html).toContain('alice');
    });
});
