import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import type { LucideIcon } from 'lucide-react';
import { Home, Radio, Sparkles, FolderTree, Settings, Logs, Users, Share2, Wallet, KeyRound, ClipboardList, Gauge, HeartPulse } from 'lucide-react';
import type { Role } from '@/api/user';

// Page 表示应用支持的固定页面集合。
export type Page = 'home' | 'channel' | 'group' | 'model' | 'users' | 'audit' | 'ratelimit' | 'ops' | 'share' | 'billing' | 'apikey' | 'log' | 'setting';

// NavItem 描述导航按钮使用的页面标识、文案和图标。
type NavItem = { id: Page; label: string; icon: LucideIcon };

// NAV_ITEMS 是全部页面的固定导航定义，顺序即导航顺序；按角色裁剪见 navItemsFor。
export const NAV_ITEMS: NavItem[] = [
    { id: 'home', label: 'Home', icon: Home },
    { id: 'channel', label: 'Channel', icon: Radio },
    { id: 'group', label: 'Group', icon: FolderTree },
    { id: 'model', label: 'Model', icon: Sparkles },
    { id: 'users', label: 'Users', icon: Users },
    { id: 'audit', label: 'Audit', icon: ClipboardList },
    { id: 'ratelimit', label: 'RateLimit', icon: Gauge },
    { id: 'ops', label: 'Ops', icon: HeartPulse },
    { id: 'share', label: 'Share', icon: Share2 },
    { id: 'billing', label: 'Billing', icon: Wallet },
    { id: 'apikey', label: 'API Key', icon: KeyRound },
    { id: 'log', label: 'Log', icon: Logs },
    { id: 'setting', label: 'Setting', icon: Settings },
];

// NAV_PAGES_BY_ROLE 按角色声明可见页面: 用户无渠道与管理后台, 渠道商多渠道与发布, 管理员全量。
const NAV_PAGES_BY_ROLE: Record<string, Page[]> = {
    admin: ['home', 'channel', 'group', 'model', 'users', 'audit', 'ratelimit', 'ops', 'share', 'billing', 'apikey', 'log', 'setting'],
    reseller: ['home', 'channel', 'group', 'share', 'billing', 'apikey', 'log', 'setting'],
    user: ['home', 'group', 'billing', 'apikey', 'log', 'setting'],
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
    setCurrentPage: (page: Page) => void; // 切换当前页面。
}

// useAppStore 保存应用当前页面及页面名称切换方向。
export const useAppStore = create<AppState>()(
    persist(
        (set, get) => ({
            currentPage: 'home',
            direction: 0,
            setCurrentPage: (page) => {
                const currentIndex = NAV_ORDER.indexOf(get().currentPage);
                const nextIndex = NAV_ORDER.indexOf(page);
                set({ currentPage: page, direction: nextIndex > currentIndex ? 1 : -1 });
            },
        }),
        {
            name: 'nav-storage',
        }
    )
);
