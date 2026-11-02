import { useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Gauge } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { useSetSetting, useSettingList, SettingKey } from '@/api/setting';
import {
    LIMIT_FIELDS,
    USER_TIERS,
    draftFromPolicies,
    parseUserTierPolicies,
    policiesFromDraft,
    serializeUserTierPolicies,
    validateTierDraft,
    type LimitField,
    type UserTier,
} from '@/lib/user-tier';

// 用户等级配额卡片。
// 数据形状是一段 JSON(user_tier_policies), 但让管理员直接编 JSON 迟早会写坏一个括号
// 然后整段配额静默失效 —— 这里用四个等级 × 三个数字框的结构化表单代替,
// 保存时序列化回 JSON。校验规则(非负整数)与后端 ParseUserTierPolicies 同口径。

// LIMIT_LABEL_KEYS 三个维度在界面上的标签与说明键。
const LIMIT_LABEL_KEYS: Record<LimitField, { label: string; hint: string }> = {
    rpm: { label: 'rpm', hint: 'rpmHint' },
    tpm: { label: 'tpm', hint: 'tpmHint' },
    concurrent: { label: 'concurrent', hint: 'concurrentHint' },
};

export function SettingTierPolicies() {
    const t = useTranslations('setting.tierPolicies');
    const tRoot = useTranslations('setting');
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();

    // 服务端值派生草稿起点: 空串从未配置时按后端默认配额展示, 让管理员有个可解释的起点。
    const raw = settings?.find((item) => item.key === SettingKey.UserTierPolicies)?.value ?? '';
    const parsed = useMemo(() => parseUserTierPolicies(raw), [raw]);

    // 草稿以 parsed 为基准; 用户一旦编辑就切到本地草稿(record 由字符串组成, 便于中间态 "1" 之类)。
    const [draft, setDraft] = useState<Record<UserTier, Record<LimitField, string>> | null>(null);
    const baseDraft = useMemo(() => draftFromPolicies(parsed.policies), [parsed.policies]);
    const values = draft ?? baseDraft;

    // 解析失败时给出明确提示: 静默展示默认值会让管理员以为旧的那套还在生效。
    const parseErrorKey = parsed.error === ''
        ? ''
        : parsed.error === 'invalidJson'
            ? 'errorInvalidJson'
            : parsed.error === 'invalidTier'
                ? 'errorInvalidTier'
                : parsed.error === 'invalidLimit'
                    ? 'errorInvalidLimit'
                    : 'errorInvalidShape';

    const setField = (tier: UserTier, field: LimitField, value: string) => {
        // 只留数字, 从源头挡住 "-"/"."/字母; 空串允许(用于清空后重填), 校验时再拦。
        const sanitized = value.replace(/[^0-9]/g, '');
        setDraft((previous) => {
            const next = { ...(previous ?? baseDraft) };
            next[tier] = { ...next[tier], [field]: sanitized };
            return next;
        });
    };

    const invalid = validateTierDraft(values);
    // 只以"当前草稿是否合法"为准, 不受服务端旧值是否损坏影响:
    // 已保存的 JSON 坏了时, 管理员恰恰需要能用这张表单把它覆盖成正确的值,
    // 否则就只能在数据库里手改 —— 提示里那句"修正或重置为默认值后再保存"也就成了空话。
    const canSave = invalid === null;

    const save = () => {
        const bad = validateTierDraft(values);
        if (bad) {
            // 精确指出哪一档哪一项不合格, 而不是笼统"输入有误"。
            toast.error(t('invalidField', { tier: t('tier.' + bad.tier), field: t('field.' + bad.field) }));
            return;
        }
        const value = serializeUserTierPolicies(policiesFromDraft(values));
        setSetting.mutate({ key: SettingKey.UserTierPolicies, value }, {
            onSuccess: () => {
                setDraft(null);
                toast.success(tRoot('saved'));
            },
            onError: (error) => toast.error(error instanceof Error ? error.message : tRoot('saveFailed')),
        });
    };

    // 重置回默认配额: 默认值写死在前端常量, 只在管理员显式点击时生效(不会自动覆盖已保存值)。
    const resetDefaults = () => {
        setDraft(draftFromPolicies(parseUserTierPolicies('').policies));
    };

    return (
        <div className="space-y-4 rounded-3xl border border-border bg-card p-6">
            <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                    <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                        <Gauge className="size-5" aria-hidden="true" />
                        {t('title')}
                    </h2>
                    <p className="text-xs text-muted-foreground">{t('hint')}</p>
                </div>
            </div>

            {parseErrorKey !== '' && (
                <p className="rounded-2xl border border-destructive/40 bg-destructive/10 px-4 py-3 text-xs text-destructive">
                    {t(parseErrorKey)}
                </p>
            )}

            <div className="overflow-x-auto rounded-2xl border border-border">
                <table className="w-full min-w-[34rem] text-sm">
                    <thead className="text-left text-xs text-muted-foreground">
                        <tr className="border-b border-border">
                            <th className="px-4 py-2 font-medium">{t('colTier')}</th>
                            {LIMIT_FIELDS.map((field) => (
                                <th key={field} className="px-4 py-2 font-medium">
                                    {t('field.' + field)}
                                    <span className="ml-1 text-[10px] text-muted-foreground/80">{t(LIMIT_LABEL_KEYS[field].hint)}</span>
                                </th>
                            ))}
                        </tr>
                    </thead>
                    <tbody>
                        {USER_TIERS.map((tier) => (
                            <tr key={tier} className="border-b border-border/60 last:border-0">
                                <td className="px-4 py-2 font-medium text-card-foreground">{t('tier.' + tier)}</td>
                                {LIMIT_FIELDS.map((field) => (
                                    <td key={field} className="px-4 py-2">
                                        <Input
                                            type="number"
                                            min={0}
                                            step={1}
                                            inputMode="numeric"
                                            className="h-9 w-24 rounded-xl"
                                            aria-label={t('tier.' + tier) + ' ' + t('field.' + field)}
                                            value={values[tier][field]}
                                            onChange={(event) => setField(tier, field, event.target.value)}
                                        />
                                    </td>
                                ))}
                            </tr>
                        ))}
                    </tbody>
                </table>
            </div>

            <p className="text-xs text-muted-foreground">{t('zeroHint')}</p>

            <div className="flex justify-end gap-2 border-t border-border/60 pt-3">
                <Button type="button" variant="secondary" className="rounded-xl h-9 px-4" onClick={resetDefaults}>
                    {t('resetDefaults')}
                </Button>
                <Button type="button" className="rounded-xl h-9 px-4" onClick={save} disabled={!canSave || setSetting.isPending}>
                    {setSetting.isPending ? t('saving') : t('save')}
                </Button>
            </div>
        </div>
    );
}
