import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { Filter, Loader, Save } from 'lucide-react';
import { Input } from '@/components/ui/input';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { IconButton } from '@/components/common/IconButton';
import { ruleIsEmpty, useSaveModelAutoAddRule, type ModelAutoAddRule } from '@/api/model-rule';
import { cn } from '@/lib/utils';

// EMPTY_RULE 是「什么都没设」的初值: 每个字段空串即回落全局默认。
const EMPTY_RULE: ModelAutoAddRule = {
    mode: '',
    price_min: '',
    price_max: '',
    markup_min: '',
    markup_max: '',
    exclude: '',
    include: '',
    fill_price: '',
};

// 跟随全局在三态选择里用一个哨兵值表示 —— Radix Select 不接受空串当选项值。
const FOLLOW = 'follow';

// ModelRuleEditor 是单个模型的自动添加条件编辑器。
//
// 为什么做成弹层挂在模型卡片/模型行上而不是设置页的一张表: 这些条件天然是「按模型」的,
// 放在模型旁边才能一眼看到「这个模型的规则是什么」。系统设置里那一组保留为全局默认,
// 这里任何一项留空都表示「跟着全局走」, 只有填了的那一项才覆盖。
export function ModelRuleEditor({
    modelName,
    rule,
    trigger,
}: {
    modelName: string;
    rule: ModelAutoAddRule | undefined;
    trigger?: React.ReactNode;
}) {
    const t = useTranslations('model.rule');
    const save = useSaveModelAutoAddRule();
    const [open, setOpen] = useState(false);
    const [draft, setDraft] = useState<ModelAutoAddRule>(rule ?? EMPTY_RULE);

    // 弹层每次打开都从最新服务端值重新起草: 上一次取消留下的改动不该带进来。
    const handleOpenChange = (next: boolean) => {
        if (next) setDraft(rule ?? EMPTY_RULE);
        setOpen(next);
    };

    const submit = () => {
        save.mutate(
            { name: modelName, rule: draft },
            {
                onSuccess: () => {
                    setOpen(false);
                    toast.success(t('saved'));
                },
                onError: (error) => toast.error(t('saveFailed'), { description: error.message }),
            }
        );
    };

    const field = (key: keyof ModelAutoAddRule, label: string, placeholder: string, type = 'text') => (
        <label className="grid gap-1 text-xs text-muted-foreground">
            {label}
            <Input
                type={type}
                step="any"
                value={draft[key]}
                placeholder={placeholder}
                onChange={(e) => setDraft({ ...draft, [key]: e.target.value })}
                className="h-8 text-sm rounded-lg"
            />
        </label>
    );

    return (
        <Popover open={open} onOpenChange={handleOpenChange}>
            <PopoverTrigger asChild>
                {trigger ?? (
                    <IconButton className="size-7" tip={t('open')}>
                        <Filter className="size-3.5" />
                    </IconButton>
                )}
            </PopoverTrigger>
            <PopoverContent align="end" className="w-80 rounded-xl p-3">
                <div className="flex items-center justify-between mb-2">
                    <span className="text-sm font-semibold text-card-foreground truncate">{modelName}</span>
                    <span className="text-xs text-muted-foreground shrink-0">{t('title')}</span>
                </div>

                <div className="grid gap-2">
                    <label className="grid gap-1 text-xs text-muted-foreground">
                        {t('mode')}
                        <Select
                            value={draft.mode === '' ? FOLLOW : draft.mode}
                            onValueChange={(value) =>
                                setDraft({ ...draft, mode: value === FOLLOW ? '' : (value as ModelAutoAddRule['mode']) })
                            }
                        >
                            <SelectTrigger className="h-8 rounded-lg text-sm"><SelectValue /></SelectTrigger>
                            <SelectContent className="rounded-lg">
                                <SelectItem value={FOLLOW}>{t('modeFollow')}</SelectItem>
                                <SelectItem value="on">{t('modeOn')}</SelectItem>
                                <SelectItem value="off">{t('modeOff')}</SelectItem>
                            </SelectContent>
                        </Select>
                    </label>

                    <div className="grid grid-cols-2 gap-2">
                        {field('price_min', t('priceMin'), t('followHint'), 'number')}
                        {field('price_max', t('priceMax'), t('followHint'), 'number')}
                        {field('markup_min', t('markupMin'), t('followHint'), 'number')}
                        {field('markup_max', t('markupMax'), t('followHint'), 'number')}
                    </div>
                    {field('include', t('include'), t('regexHint'))}
                    {field('exclude', t('exclude'), t('regexHint'))}

                    <label className="grid gap-1 text-xs text-muted-foreground">
                        {t('fillPrice')}
                        <Select
                            value={draft.fill_price === '' ? FOLLOW : draft.fill_price}
                            onValueChange={(value) =>
                                setDraft({ ...draft, fill_price: value === FOLLOW ? '' : (value as 'on' | 'off') })
                            }
                        >
                            <SelectTrigger className="h-8 rounded-lg text-sm"><SelectValue /></SelectTrigger>
                            <SelectContent className="rounded-lg">
                                <SelectItem value={FOLLOW}>{t('modeFollow')}</SelectItem>
                                <SelectItem value="on">{t('switchOn')}</SelectItem>
                                <SelectItem value="off">{t('switchOff')}</SelectItem>
                            </SelectContent>
                        </Select>
                    </label>
                </div>

                <p className="mt-2 text-[11px] leading-snug text-muted-foreground">{t('hint')}</p>

                <div className="flex items-center justify-between gap-2 mt-2">
                    <span className={cn('text-[11px] text-muted-foreground', ruleIsEmpty(draft) && 'opacity-60')}>
                        {ruleIsEmpty(draft) ? t('empty') : t('customized')}
                    </span>
                    <button
                        type="button"
                        onClick={submit}
                        disabled={save.isPending}
                        className="h-8 px-3 flex items-center gap-1.5 rounded-lg bg-primary text-primary-foreground text-xs font-medium transition-all hover:bg-primary/90 active:scale-[0.98] disabled:opacity-50"
                    >
                        {save.isPending ? <Loader className="size-3.5 animate-spin" /> : <Save className="size-3.5" />}
                        {t('save')}
                    </button>
                </div>
            </PopoverContent>
        </Popover>
    );
}
