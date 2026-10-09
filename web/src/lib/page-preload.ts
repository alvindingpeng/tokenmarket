import type { Page } from '@/stores/app';

// pageImports 统一定义页面懒加载和导航预加载使用的模块入口。
export const pageImports = {
    home: () => import('@/components/modules/home'),
    channel: () => import('@/components/modules/channel'),
    group: () => import('@/components/modules/group'),
    model: () => import('@/components/modules/model'),
    users: () => import('@/components/modules/users'),
    audit: () => import('@/components/modules/audit'),
    ratelimit: () => import('@/components/modules/ratelimit'),
    ops: () => import('@/components/modules/ops'),
    share: () => import('@/components/modules/share'),
    billing: () => import('@/components/modules/billing'),
    apikey: () => import('@/components/modules/apikey'),
    log: () => import('@/components/modules/log'),
    setting: () => import('@/components/modules/setting'),
};

// preloadPage 根据导航意图提前加载对应页面模块。
export function preloadPage(page: Page) {
    void pageImports[page]();
}
