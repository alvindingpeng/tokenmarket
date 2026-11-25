import { useTheme } from '@/provider/theme';
import { useTranslations } from 'use-intl';
import { Sun, Moon, Monitor, PanelsTopLeft } from 'lucide-react';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';

export function SettingAppearance() {
    const t = useTranslations('setting');
    const { theme, setTheme } = useTheme();

    return (
        <div className="rounded-3xl border border-border bg-card p-4 sm:p-6 space-y-5">
            <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                <Sun className="h-5 w-5" />
                {t('appearance')}
            </h2>

            {/* 主题 */}
            <div className="flex items-center flex-wrap justify-between gap-3">
                <div className="flex items-center gap-3">
                    {theme === 'dark' ? <Moon className="h-5 w-5 text-muted-foreground" /> : <Sun className="h-5 w-5 text-muted-foreground" />}
                    <span className="text-sm font-medium">{t('theme.label')}</span>
                </div>
                <Select value={theme} onValueChange={setTheme}>
                    <SelectTrigger aria-label={t('theme.label')} className="max-w-full rounded-xl">
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent className="rounded-xl">
                        <SelectItem value="light" className="rounded-xl">
                            <Sun className="size-4" />
                            {t('theme.light')}
                        </SelectItem>
                        <SelectItem value="dark" className="rounded-xl">
                            <Moon className="size-4" />
                            {t('theme.dark')}
                        </SelectItem>
                        <SelectItem value="system" className="rounded-xl">
                            <Monitor className="size-4" />
                            {t('theme.system')}
                        </SelectItem>
                        <SelectItem value="white" className="rounded-xl">
                            <PanelsTopLeft className="size-4" />
                            {t('theme.white')}
                        </SelectItem>
                    </SelectContent>
                </Select>
            </div>

            {/* 语言切换已移到网页右上方(控制台顶栏 LanguageSwitch), 这里只保留外观/主题设置。 */}
        </div>
    );
}
