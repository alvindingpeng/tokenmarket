import { useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Dices, RefreshCw, ShieldCheck } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { useSettingList, useSetSetting, SettingKey } from '@/api/setting';
import { toast } from 'sonner';
import { cn } from '@/lib/utils';

// CHANNEL_KEYS 是本卡片管理的设置键清单, 与后端 model/setting.go 对齐。
const CHANNEL_KEYS = [
    SettingKey.ChannelObservationDays,
    SettingKey.ChannelObservationMinSamples,
    SettingKey.ChannelObservationSuccessFloor,
    SettingKey.AutoAddPriceMin,
    SettingKey.AutoAddPriceMax,
    SettingKey.AutoAddMarkupMin,
    SettingKey.AutoAddMarkupMax,
    SettingKey.AutoAddExclude,
    SettingKey.AutoAddInclude,
    SettingKey.AutoAddFillPrice,
    SettingKey.AutoAddBackground,
    SettingKey.AutoAddInterval,
] as const;

// rowClass 是「说明在左、控件在右」的统一行布局, 与运维设置卡片保持一致。
const rowClass = 'flex items-start justify-between gap-4 py-3';
const dividerClass = 'border-t border-border/60';

// SettingChannel 渠道策略设置: 观察期毕业门槛(P2)与模型自动添加条件(B4)。
// 两组键放在同一张卡片: 它们都只影响「渠道准入之后的自动化行为」, 拆成两张卡会让人找不到关联。
export function SettingChannel() {
    const t = useTranslations('channel.settings');
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();

    // 服务端设置是唯一权威: 已保存值直接派生, 本地只存草稿。
    // 与运维卡片同一模式 —— 用 effect 把服务端值搬进 state 会覆盖用户正在输入的内容。
    const savedValues = useMemo(() => {
        const next: Record<string, string> = {};
        for (const key of CHANNEL_KEYS) {
            const found = settings?.find((item) => item.key === key);
            next[key] = found ? found.value : '';
        }
        return next;
    }, [settings]);
    const [draft, setDraft] = useState<Record<string, string>>({});
    const values = useMemo(() => ({ ...savedValues, ...draft }), [savedValues, draft]);

    // save 持久化单个设置键; 与已保存值相同则跳过, 失败把草稿回退到服务端值。
    const save = (key: string, value: string) => {
        if (savedValues[key] === value) return;
        setSetting.mutate(
            { key, value },
            {
                onSuccess: () => toast.success(t('saved')),
                onError: (error) => {
                    setDraft((prev) => ({ ...prev, [key]: savedValues[key] ?? '' }));
                    toast.error(error instanceof Error ? error.message : t('saveFailed'));
                },
            }
        );
    };

    // saveNumber 数字输入: 失焦时夹取范围并保存。
    // 受控输入必须挂 onChange 只写草稿、失焦才提交, 否则 React 会把每次按键都回滚掉。
    const saveNumber = (key: string, raw: string, min: number, max: number, fallback: string) => {
        const parsed = Number.parseFloat(raw);
        const normalized = Number.isFinite(parsed) ? String(Math.min(Math.max(parsed, min), max)) : fallback;
        setDraft((prev) => ({ ...prev, [key]: normalized }));
        save(key, normalized);
    };

    const boolValue = (key: string) => (values[key] ?? '') === 'true';

    // numberRow 复用同一段 JSX: 六个数字输入只差 key / 范围 / 兜底值 / 文案。
    const numberRow = (
        key: string,
        id: string,
        label: string,
        hint: string,
        min: number,
        max: number,
        step: number,
        fallback: string
    ) => (
        <div className={rowClass + ' ' + dividerClass} key={key}>
            <div className="min-w-0">
                <Label htmlFor={id} className="text-sm font-medium">
                    {label}
                </Label>
                <p className="text-xs text-muted-foreground">{hint}</p>
            </div>
            <Input
                id={id}
                type="number"
                step={step}
                min={min}
                max={max}
                className="h-9 w-28 shrink-0 rounded-xl"
                value={values[key] ?? ''}
                onChange={(event) => setDraft((prev) => ({ ...prev, [key]: event.target.value }))}
                onBlur={(event) => saveNumber(key, event.target.value, min, max, fallback)}
            />
        </div>
    );

    return (
        <div className="space-y-1 rounded-3xl border border-border bg-card p-6">
            <h2 className="flex items-center gap-2 pb-2 text-lg font-bold text-card-foreground">
                <ShieldCheck className="size-5" />
                {t('observation.title')}
            </h2>
            <p className="pb-1 text-xs text-muted-foreground">{t('observation.hint')}</p>
            {numberRow(SettingKey.ChannelObservationDays, 'obs-days', t('observation.days'), t('observation.daysHint'), 1, 3650, 1, '7')}
            {numberRow(
                SettingKey.ChannelObservationMinSamples,
                'obs-samples',
                t('observation.minSamples'),
                t('observation.minSamplesHint'),
                1,
                1000000,
                1,
                '100'
            )}
            {numberRow(
                SettingKey.ChannelObservationSuccessFloor,
                'obs-floor',
                t('observation.successFloor'),
                t('observation.successFloorHint'),
                0.01,
                1,
                0.01,
                '0.98'
            )}

            <h2 className="flex items-center gap-2 pb-2 pt-6 text-lg font-bold text-card-foreground">
                <Dices className="size-5" />
                {t('autoAdd.title')}
            </h2>
            <p className="pb-1 text-xs text-muted-foreground">{t('autoAdd.hint')}</p>
            {numberRow(SettingKey.AutoAddPriceMin, 'aa-price-min', t('autoAdd.priceMin'), '', 0, 1e9, 0.01, '0')}
            {numberRow(SettingKey.AutoAddPriceMax, 'aa-price-max', t('autoAdd.priceMax'), '', 0, 1e9, 0.01, '0')}
            {numberRow(SettingKey.AutoAddMarkupMin, 'aa-markup-min', t('autoAdd.markupMin'), t('autoAdd.markupHint'), 0, 1e6, 0.01, '0')}
            {numberRow(SettingKey.AutoAddMarkupMax, 'aa-markup-max', t('autoAdd.markupMax'), '', 0, 1e6, 0.01, '0')}

            <div className={rowClass + ' ' + dividerClass}>
                <div className="min-w-0">
                    <Label htmlFor="aa-exclude" className="text-sm font-medium">
                        {t('autoAdd.exclude')}
                    </Label>
                    <p className="text-xs text-muted-foreground">{t('autoAdd.excludeHint')}</p>
                </div>
                <Input
                    id="aa-exclude"
                    placeholder="^gpt-3|legacy"
                    className="h-9 w-full max-w-md rounded-xl"
                    value={values[SettingKey.AutoAddExclude] ?? ''}
                    onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.AutoAddExclude]: event.target.value }))}
                    onBlur={(event) => save(SettingKey.AutoAddExclude, event.target.value)}
                />
            </div>

            <div className={rowClass + ' ' + dividerClass}>
                <div className="min-w-0">
                    <Label htmlFor="aa-include" className="text-sm font-medium">
                        {t('autoAdd.include')}
                    </Label>
                    <p className="text-xs text-muted-foreground">{t('autoAdd.includeHint')}</p>
                </div>
                <Input
                    id="aa-include"
                    placeholder="^gpt-4o"
                    className="h-9 w-full max-w-md rounded-xl"
                    value={values[SettingKey.AutoAddInclude] ?? ''}
                    onChange={(event) => setDraft((prev) => ({ ...prev, [SettingKey.AutoAddInclude]: event.target.value }))}
                    onBlur={(event) => save(SettingKey.AutoAddInclude, event.target.value)}
                />
            </div>

            <div className={cn(rowClass, dividerClass)}>
                <div className="min-w-0">
                    <Label className="text-sm font-medium">{t('autoAdd.fillPrice')}</Label>
                    <p className="text-xs text-muted-foreground">{t('autoAdd.fillPriceHint')}</p>
                </div>
                <Switch
                    checked={boolValue(SettingKey.AutoAddFillPrice)}
                    onCheckedChange={(checked) => {
                        setDraft((prev) => ({ ...prev, [SettingKey.AutoAddFillPrice]: String(checked) }));
                        save(SettingKey.AutoAddFillPrice, String(checked));
                    }}
                />
            </div>

            {/* 后台自动添加: 把「保存时触发」的一次性行为升级为周期复查。 */}
            <h2 className="flex items-center gap-2 pb-2 pt-6 text-lg font-bold text-card-foreground">
                <RefreshCw className="size-5" />
                {t('autoAdd.background')}
            </h2>
            <p className="pb-1 text-xs text-muted-foreground">{t('autoAdd.backgroundHint')}</p>
            <div className={cn(rowClass, dividerClass)}>
                <div className="min-w-0">
                    <Label className="text-sm font-medium">{t('autoAdd.backgroundEnabled')}</Label>
                    <p className="text-xs text-muted-foreground">{t('autoAdd.backgroundEnabledHint')}</p>
                </div>
                <Switch
                    checked={boolValue(SettingKey.AutoAddBackground)}
                    onCheckedChange={(checked) => {
                        setDraft((prev) => ({ ...prev, [SettingKey.AutoAddBackground]: String(checked) }));
                        save(SettingKey.AutoAddBackground, String(checked));
                    }}
                />
            </div>
            {/* 周期只在开关打开时出现: 关着的时候它不生效, 摆在界面上只会让人以为改它有用。 */}
            {boolValue(SettingKey.AutoAddBackground) &&
                numberRow(SettingKey.AutoAddInterval, 'aa-interval', t('autoAdd.interval'), t('autoAdd.intervalHint'), 1, 168, 1, '24')}
        </div>
    );
}
