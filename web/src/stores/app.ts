import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import type { LucideIcon } from 'lucide-react';
import { Home, Radio, Sparkles, FolderTree, Settings, Logs, Share2, Wallet, KeyRound, HeartPulse, Coins, Landmark, Bell, BarChart3 } from 'lucide-react';
import type { Role } from '@/api/user';

// Page 是 Dock 一级导航的页面集合; 用户管理/审计记录/限流管理/兑换码已并入设置页二级菜单, 不再是独立页面。
export type Page = 'home' | 'channel' | 'group' | 'model' | 'ops' | 'share' | 'wallet' | 'withdrawals' | 'billing' | 'apikey' | 'log' | 'notification' | 'analytics' | 'setting';

// SettingSectionID 设置页二级菜单的分组标识; 分组定义见 components/modules/setting/sections.ts。
// 管理后台能力(用户/审计/限流/兑换码)与配置分组共用同一层菜单。
export type SettingSectionID = 'personal' | 'users' | 'audit' | 'ratelimit' | 'redemption' | 'site' | 'system' | 'billing' | 'ops';

// NavItem 描述导航按钮使用的页面标识、文案和图标。
type NavItem = { id: Page; label: string; icon: LucideIcon };

// NAV_ITEMS 是全部页面的固定导航定义，顺序即导航顺序；按角色裁剪见 navItemsFor。
export const NAV_ITEMS: NavItem[] = [
    { id: 'home', label: 'Home', icon: Home },
    { id: 'channel', label: 'Channel', icon: Radio },
    { id: 'group', label: 'Group', icon: FolderTree },
    { id: 'model', label: 'Model', icon: Sparkles },
    { id: 'ops', label: 'Ops', icon: HeartPulse },
    { id: 'share', label: 'Share', icon: Share2 },
    { id: 'wallet', label: 'Wallet', icon: Coins },
    { id: 'withdrawals', label: 'Withdrawals', icon: Landmark },
    { id: 'billing', label: 'Billing', icon: Wallet },
    { id: 'apikey', label: 'API Key', icon: KeyRound },
    { id: 'log', label: 'Log', icon: Logs },
    { id: 'notification', label: 'Notification', icon: Bell },
    { id: 'analytics', label: 'Analytics', icon: BarChart3 },
    { id: 'setting', label: 'Setting', icon: Settings },
];

// NAV_PAGES_BY_ROLE 按角色声明可见页面: 用户无渠道与管理后台, 渠道商多渠道与发布, 管理员全量。
const NAV_PAGES_BY_ROLE: Record<string, Page[]> = {
    admin: ['home', 'channel', 'group', 'model', 'ops', 'share', 'wallet', 'withdrawals', 'billing', 'apikey', 'log', 'notification', 'analytics', 'setting'],
    reseller: ['home', 'channel', 'group', 'share', 'wallet', 'billing', 'apikey', 'log', 'notification', 'analytics', 'setting'],
    user: ['home', 'group', 'wallet', 'billing', 'apikey', 'log', 'notification', 'analytics', 'setting'],
};

// navItemsFor 返回角色可见的导航项，按 NAV_ITEMS 的全局顺序排列。
export function navItemsFor(role: Role | null): NavItem[] {
    const pages = NAV_PAGES_BY_ROLE[role ?? 'user'] ?? NAV_PAGES_BY_ROLE.user;
    return NAV_ITEMS.filter((item) => pages.includes(item.id));
}

const NAV_ORDER: Page[] = NAV_ITEMS.map((item) => item.id); // NAV_ORDER 用于计算页面名称滚动方向。

interface AppState {
    currentPage: Page; // 当前选中的固定页面。
    direction: number; // 页面名称切换时的滚动方向。
    settingSection: SettingSectionID; // 设置页二级菜单当前分组(持久化, 重开仍在原分组)。
    setCurrentPage: (page: Page) => void; // 切换当前页面。
    setSettingSection: (section: SettingSectionID) => void; // 切换设置页分组。
}

// useAppStore 保存应用当前页面及页面名称切换方向。
export const useAppStore = create<AppState>()(
    persist(
        (set, get) => ({
            currentPage: 'home',
            direction: 0,
            settingSection: 'personal',
            setCurrentPage: (page) => {
                const currentIndex = NAV_ORDER.indexOf(get().currentPage);
                const nextIndex = NAV_ORDER.indexOf(page);
                set({ currentPage: page, direction: nextIndex > currentIndex ? 1 : -1 });
            },
            setSettingSection: (section) => set({ settingSection: section }),
        }),
        {
            name: 'nav-storage',
        }
    )
);
