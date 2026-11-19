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
import { ruleIsEmpty, useModelAutoAddRules, useSaveModelAutoAddRule, type ModelAutoAddRule } from '@/api/model-rule';
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
//
// 模型名有两种来源: 卡片/成员行把名字定死传进来(modelName), 模型配置页的成员来自不同渠道、
// 名字不一定在任何一张模型表里, 所以那里留空由用户填(nameEntry), 边填边认已有的那份规则。
export function ModelRuleEditor({
    modelName,
    rule,
    trigger,
    nameEntry = false,
}: {
    modelName?: string;
    rule: ModelAutoAddRule | undefined;
    trigger?: React.ReactNode;
    nameEntry?: boolean; // 为真时弹层顶部多一个模型名输入框, 并忽略 modelName。
}) {
    const t = useTranslations('model.rule');
    const save = useSaveModelAutoAddRule();
    const { data: rules } = useModelAutoAddRules();
    const [open, setOpen] = useState(false);
    const [name, setName] = useState('');
    const [draft, setDraft] = useState<ModelAutoAddRule>(rule ?? EMPTY_RULE);

    const trimmed = name.trim();
    // 由用户填名字时, 当前生效的名字与它对应的规则都要现算: 手输的名字未必在任何列表里。
    const activeName = nameEntry ? trimmed : (modelName ?? '');

    // 弹层每次打开都从最新服务端值重新起草: 上一次取消留下的改动不该带进来。
    const handleOpenChange = (next: boolean) => {
        if (next) {
            setDraft(rule ?? EMPTY_RULE);
            setName('');
        }
        setOpen(next);
    };

    // 改名就换成那个模型自己的条件; 名字还没落到某个模型上时按「没设过」起草。
    const handleNameChange = (next: string) => {
        setName(next);
        setDraft(rules?.get(next.trim().toLowerCase()) ?? EMPTY_RULE);
    };

    const submit = () => {
        if (!activeName) return;
        save.mutate(
            { name: activeName, rule: draft },
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
            <PopoverContent align={nameEntry ? 'center' : 'end'} className="w-80 rounded-xl p-3">
                <div className="flex items-center justify-between mb-2">
                    {nameEntry ? (
                        <span className="text-sm font-semibold text-card-foreground shrink-0">{t('title')}</span>
                    ) : (
                        <span className="text-sm font-semibold text-card-foreground truncate">{activeName}</span>
                    )}
                    {!nameEntry && <span className="text-xs text-muted-foreground shrink-0">{t('title')}</span>}
                </div>

                {nameEntry && (
                    <label className="mb-2 grid gap-1 text-xs text-muted-foreground">
                        {t('name')}
                        <Input
                            value={name}
                            placeholder={t('namePlaceholder')}
                            onChange={(e) => handleNameChange(e.target.value)}
                            className="h-8 text-sm rounded-lg"
                        />
                        <span className="text-[11px] leading-snug opacity-80">{t('nameHint')}</span>
                    </label>
                )}

                <div className={cn('grid gap-2', !activeName && 'pointer-events-none opacity-50')}>
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
                        {nameEntry && activeName && (
                            <span className="ml-1 text-foreground">{activeName}</span>
                        )}
                    </span>
                    <button
                        type="button"
                        onClick={submit}
                        disabled={save.isPending || !activeName}
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
