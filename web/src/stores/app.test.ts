import { describe, expect, it } from 'vitest';
import { NAV_ITEMS, navItemsFor, type Page } from './app';
import { pageImports } from '@/lib/page-preload';
import type { Role } from '@/api/user';

// ALL_ROLES 全角色遍历用; null 表示未登录回落到用户视图。
const ALL_ROLES: (Role | null)[] = ['admin', 'reseller', 'user', null];

// 导航一致性: 新增页面时最容易漏的是「加了 NAV_ITEMS 忘了懒加载映射」或「角色裁剪漏配」,
// 前者表现为点导航白屏, 后者表现为某角色凭空少一个功能入口(或多一个进不去的)。
describe('navigation', () => {
    it('导航项与懒加载模块一一对应且 id 唯一', () => {
        const navIds = NAV_ITEMS.map((item) => item.id);
        expect(new Set(navIds).size).toBe(navIds.length);
        expect(navIds.slice().sort()).toEqual(Object.keys(pageImports).slice().sort());
    });

    it('工单页对全部角色可见', () => {
        for (const role of ALL_ROLES) {
            expect(navItemsFor(role).map((item) => item.id)).toContain('tickets');
        }
    });

    it('角色裁剪: 用户看不到渠道与运维, 管理员看到全部导航', () => {
        const userPages = navItemsFor('user').map((item) => item.id);
        expect(userPages).not.toContain('channel');
        expect(userPages).not.toContain('ops');
        expect(userPages).not.toContain('withdrawals');
        expect(navItemsFor('admin')).toHaveLength(NAV_ITEMS.length);
    });

    it('各角色导航顺序都跟随 NAV_ITEMS 全局顺序', () => {
        const globalOrder = NAV_ITEMS.map((item) => item.id);
        for (const role of ALL_ROLES) {
            const ids = navItemsFor(role).map((item) => item.id);
            expect(ids).toEqual(globalOrder.filter((id: Page) => ids.includes(id)));
        }
    });
});
