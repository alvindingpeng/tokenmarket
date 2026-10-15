import type { LucideIcon } from 'lucide-react';
import { CreditCard, Globe, HeartPulse, Monitor, UserRound } from 'lucide-react';
import type { SettingSectionID } from '@/stores/app';

// SettingCardID 设置页里的每张功能卡片; 新增卡片必须挂到某个分组, 否则测试会失败。
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

// SettingSection 一个二级菜单分组: 图标 + 文案 key + 可见角色 + 组内卡片(顺序即展示顺序)。
export type SettingSection = {
    id: SettingSectionID;
    icon: LucideIcon;
    adminOnly: boolean;
    cards: SettingCardID[];
};

// SETTING_SECTIONS 设置页二级菜单定义: 按"谁用、管什么"分组, 顺序即菜单顺序。
// 个人分组对所有角色可见(外观/账户), 其余都是管理后台能力。
export const SETTING_SECTIONS: SettingSection[] = [
    { id: 'personal', icon: UserRound, adminOnly: false, cards: ['appearance', 'account'] },
    { id: 'site', icon: Globe, adminOnly: true, cards: ['systemInfo', 'info'] },
    { id: 'system', icon: Monitor, adminOnly: true, cards: ['system', 'platform'] },
    { id: 'billing', icon: CreditCard, adminOnly: true, cards: ['payment', 'llmPrice'] },
    { id: 'ops', icon: HeartPulse, adminOnly: true, cards: ['log', 'ops', 'backup'] },
];

// sectionsFor 按角色返回可见分组: 普通用户只看到个人设置, 渠道商与管理员看全量。
export function sectionsFor(role: string | null | undefined): SettingSection[] {
    if (role === 'admin') return SETTING_SECTIONS;
    return SETTING_SECTIONS.filter((section) => !section.adminOnly);
}
