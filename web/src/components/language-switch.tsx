import { useTranslations } from 'use-intl';
import { useSettingStore, type Locale } from '@/stores/setting';
import { cn } from '@/lib/utils';

const OPTIONS: { value: Locale; label: string }[] = [
    { value: 'zh_hans', label: '简' },
    { value: 'zh_hant', label: '繁' },
    { value: 'en', label: 'EN' },
];

// LanguageSwitch 简/繁/EN 胶囊分段开关: 落地页页头与控制台页头(网页右上方)共用同一实现。
// 复用全局语言偏好(useSettingStore): 切换立即由 LocaleProvider 用新语言重新渲染。
// 完整语言名取自 setting.language.*, 用作悬停提示与分组无障碍标签(与「设置 > 外观」同一套文案)。
export function LanguageSwitch() {
    const t = useTranslations('setting');
    const { locale, setLocale } = useSettingStore();
    return (
        <div
            className="flex shrink-0 items-center rounded-full border border-border bg-background p-0.5"
            role="group"
            aria-label={t('language.label')}
        >
            {OPTIONS.map((option) => (
                <button
                    key={option.value}
                    type="button"
                    onClick={() => setLocale(option.value)}
                    title={t('language.' + option.value)}
                    aria-pressed={locale === option.value}
                    className={cn(
                        'min-h-7 rounded-full px-2.5 py-1 text-xs font-medium transition-colors',
                        locale === option.value
                            ? 'bg-primary text-primary-foreground'
                            : 'text-muted-foreground hover:text-foreground'
                    )}
                >
                    {option.label}
                </button>
            ))}
        </div>
    );
}
