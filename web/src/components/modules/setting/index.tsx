import { Fragment, lazy, Suspense, type ComponentType, type LazyExoticComponent, type ReactNode } from 'react';
import { useTranslations } from 'use-intl';
import { Loader2 } from 'lucide-react';
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
import { SettingChannel } from './Channel';
import { SettingPayment } from './Payment';
import { sectionsFor, type SettingCardID, type SettingViewID } from './sections';

// SETTING_CARDS 配置卡片 id 到组件的映射; 分组定义在 sections.ts, 这里只负责渲染。
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
    channel: () => <SettingChannel />,
};

// SETTING_VIEW_IMPORTS 管理视图的懒加载入口; 既用于渲染, 也用于切分组前预加载。
const SETTING_VIEW_IMPORTS: Record<SettingViewID, () => Promise<{ default: ComponentType }>> = {
    users: () => import('@/components/modules/users'),
    audit: () => import('@/components/modules/audit'),
    ratelimit: () => import('@/components/modules/ratelimit'),
    redemption: () => import('@/components/modules/redemption'),
};

// SETTING_VIEWS 管理视图组件: 自带标题与滚动, 直接占满设置页正文区。
const SETTING_VIEWS: Record<SettingViewID, LazyExoticComponent<ComponentType>> = {
    users: lazy(SETTING_VIEW_IMPORTS.users),
    audit: lazy(SETTING_VIEW_IMPORTS.audit),
    ratelimit: lazy(SETTING_VIEW_IMPORTS.ratelimit),
    redemption: lazy(SETTING_VIEW_IMPORTS.redemption),
};

// Setting 渲染设置页: 顶部二级菜单按分组切页, 正文只渲染当前分组的内容。
// 配置分组渲染卡片瀑布流; 管理分组整页嵌入对应视图(视图自己管滚动, 外层不再套滚动容器)。
export function Setting() {
    const role = useAuthStore((state) => state.role);
    const section = useAppStore((state) => state.settingSection);
    const setSection = useAppStore((state) => state.setSettingSection);
    const t = useTranslations('setting');
    const sections = sectionsFor(role);
    // 角色变化或历史持久化值失效时回落到第一个可见分组, 不留空白页。
    const active = sections.find((item) => item.id === section) ?? sections[0];
    const activeView = active.views[0];
    const View = activeView ? SETTING_VIEWS[activeView] : null;

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
                            // 悬停即预加载该分组的管理视图, 切换时不再等 Suspense。
                            onMouseEnter={() => {
                                const view = item.views[0];
                                if (view) void SETTING_VIEW_IMPORTS[view]();
                            }}
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
            {View ? (
                <div className="min-h-0 flex-1 overflow-hidden">
                    <Suspense
                        fallback={
                            <div className="flex h-full items-center justify-center text-muted-foreground">
                                <Loader2 className="size-5 animate-spin" />
                            </div>
                        }
                    >
                        <View />
                    </Suspense>
                </div>
            ) : (
                <div className="h-full min-h-0 flex-1 overflow-y-auto overscroll-contain pb-24 md:pb-4">
                    <div className="columns-1 gap-4 md:columns-2 *:mb-4 *:break-inside-avoid">
                        {active.cards.map((card) => (
                            <Fragment key={card}>{SETTING_CARDS[card]()}</Fragment>
                        ))}
                    </div>
                </div>
            )}
        </div>
    );
}
