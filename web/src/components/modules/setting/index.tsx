import { Fragment, type ReactNode } from 'react';
import { useTranslations } from 'use-intl';
import { useAuthStore } from '@/api/user';
import { useAppStore } from '@/stores/app';
import { cn } from '@/lib/utils';
import { SettingAppearance } from './Appearance';
import { SettingSystem } from './System';
import { SettingSystemInfo } from './SystemInfo';
import { SettingPlatform } from './Platform';
import { SettingLLMPrice } from './LLMPrice';
import { SettingAccount } from './Account';
import { SettingInfo } from './Info';
import { SettingLog } from './Log';
import { SettingOps } from './Ops';
import { SettingBackup } from './Backup';
import { SettingPayment } from './Payment';
import { sectionsFor, type SettingCardID } from './sections';

// SETTING_CARDS 卡片 id 到组件的映射; 分组定义在 sections.ts, 这里只负责渲染。
const SETTING_CARDS: Record<SettingCardID, () => ReactNode> = {
    appearance: () => <SettingAppearance />,
    account: () => <SettingAccount />,
    systemInfo: () => <SettingSystemInfo />,
    info: () => <SettingInfo />,
    system: () => <SettingSystem />,
    platform: () => <SettingPlatform />,
    payment: () => <SettingPayment />,
    llmPrice: () => <SettingLLMPrice />,
    log: () => <SettingLog />,
    ops: () => <SettingOps />,
    backup: () => <SettingBackup />,
};

// Setting 渲染设置页: 顶部二级菜单按分组切页, 正文只渲染当前分组的卡片。
// 分组导航常驻不随卡片滚动, 移动端横向可滑动, 桌面端同样一行平铺。
export function Setting() {
    const role = useAuthStore((state) => state.role);
    const section = useAppStore((state) => state.settingSection);
    const setSection = useAppStore((state) => state.setSettingSection);
    const t = useTranslations('setting');
    const sections = sectionsFor(role);
    // 角色变化或历史持久化值失效时回落到第一个可见分组, 不留空白页。
    const active = sections.find((item) => item.id === section) ?? sections[0];

    return (
        <div className="flex h-full min-h-0 flex-col">
            <div
                role="tablist"
                aria-label={t('sections.label')}
                className="nav-scrollbar -mx-1 mb-3 flex shrink-0 items-center gap-1 overflow-x-auto overscroll-x-contain rounded-2xl border border-border bg-card p-1"
            >
                {sections.map((item) => {
                    const isActive = item.id === active.id;
                    return (
                        <button
                            key={item.id}
                            type="button"
                            role="tab"
                            aria-selected={isActive}
                            onClick={() => setSection(item.id)}
                            className={cn(
                                'flex shrink-0 items-center gap-2 rounded-xl px-3 py-2 text-sm font-medium transition-colors',
                                isActive ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground',
                            )}
                        >
                            <item.icon className="size-4" />
                            {t('sections.' + item.id)}
                        </button>
                    );
                })}
            </div>
            <div className="h-full min-h-0 flex-1 overflow-y-auto overscroll-contain pb-24 md:pb-4">
                <div className="columns-1 gap-4 md:columns-2 *:mb-4 *:break-inside-avoid">
                    {active.cards.map((card) => (
                        <Fragment key={card}>{SETTING_CARDS[card]()}</Fragment>
                    ))}
                </div>
            </div>
        </div>
    );
}
