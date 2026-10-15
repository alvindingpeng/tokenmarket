import type { LucideIcon } from 'lucide-react';
import { ClipboardList, CreditCard, Gauge, Globe, HeartPulse, Monitor, Ticket, UserRound, Users } from 'lucide-react';
import type { SettingSectionID } from '@/stores/app';

// SettingCardID 设置页里的每张配置卡片; 新增卡片必须挂到某个分组, 否则测试会失败。
export type SettingCardID =
    | 'appearance'
    | 'account'
    | 'systemInfo'
    | 'info'
    | 'system'
    | 'platform'
    | 'payment'
    | 'llmPrice'
    | 'log'
    | 'ops'
    | 'backup';

// SettingViewID 管理后台的整页视图: 自带标题与滚动, 嵌进设置页后占满正文区。
export type SettingViewID = 'users' | 'audit' | 'ratelimit' | 'redemption';

// SETTING_CARD_IDS 全部卡片清单, 供覆盖性测试与分组声明共用。
export const SETTING_CARD_IDS: SettingCardID[] = [
    'appearance',
    'account',
    'systemInfo',
    'info',
    'system',
    'platform',
    'payment',
    'llmPrice',
    'log',
    'ops',
    'backup',
];

// SETTING_VIEW_IDS 全部管理视图清单, 同样必须被某个分组恰好收录一次。
export const SETTING_VIEW_IDS: SettingViewID[] = ['users', 'audit', 'ratelimit', 'redemption'];

// SettingSection 一个二级菜单分组: 图标 + 文案 key + 可见角色 + 组内内容。
// cards 是配置卡片(瀑布流平铺), views 是管理视图(整页嵌入); 二者互斥, 一组只用其中一种。
export type SettingSection = {
    id: SettingSectionID;
    icon: LucideIcon;
    adminOnly: boolean;
    cards: SettingCardID[];
    views: SettingViewID[];
};

// SETTING_SECTIONS 设置页二级菜单定义: 顺序即菜单顺序。
// 前半是管理后台的日常操作(用户/审计/限流/兑换码), 后半是配置分组; 个人分组对所有角色可见。
export const SETTING_SECTIONS: SettingSection[] = [
    { id: 'personal', icon: UserRound, adminOnly: false, cards: ['appearance', 'account'], views: [] },
    { id: 'users', icon: Users, adminOnly: true, cards: [], views: ['users'] },
    { id: 'audit', icon: ClipboardList, adminOnly: true, cards: [], views: ['audit'] },
    { id: 'ratelimit', icon: Gauge, adminOnly: true, cards: [], views: ['ratelimit'] },
    { id: 'redemption', icon: Ticket, adminOnly: true, cards: [], views: ['redemption'] },
    { id: 'site', icon: Globe, adminOnly: true, cards: ['systemInfo', 'info'], views: [] },
    { id: 'system', icon: Monitor, adminOnly: true, cards: ['system', 'platform'], views: [] },
    { id: 'billing', icon: CreditCard, adminOnly: true, cards: ['payment', 'llmPrice'], views: [] },
    { id: 'ops', icon: HeartPulse, adminOnly: true, cards: ['log', 'ops', 'backup'], views: [] },
];

// sectionsFor 按角色返回可见分组: 普通用户只看到个人设置, 渠道商与管理员看全量。
export function sectionsFor(role: string | null | undefined): SettingSection[] {
    if (role === 'admin') return SETTING_SECTIONS;
    return SETTING_SECTIONS.filter((section) => !section.adminOnly);
}
