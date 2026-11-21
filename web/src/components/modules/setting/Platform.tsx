import { useEffect, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Percent, UserPlus, Store, ShieldCheck, Wallet, Gauge, Image as ImageIcon } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { Separator } from '@/components/ui/separator';
import { useSettingList, useSetSetting, SettingKey } from '@/api/setting';
import { toast } from 'sonner';
import { confirmedFromSettings, saveWithRollback } from './settingSave';

// PlatformRow 一行平台参数: 左侧图标与说明, 右侧控件; 数值项失焦保存, 开关即时保存。
function PlatformRow({ icon, label, hint, children }: {
    icon: React.ReactNode;
    label: string;
    hint: string;
    children: React.ReactNode;
}) {
    return (
        <div className="flex items-start gap-3 py-2">
            <span className="mt-0.5 text-primary">{icon}</span>
            <div className="min-w-0 flex-1">
                <p className="text-sm font-medium">{label}</p>
                <p className="text-xs text-muted-foreground">{hint}</p>
            </div>
            <div className="shrink-0">{children}</div>
        </div>
    );
}

// SettingPlatform 平台参数: 上浮比例、注册开关与余额规则; 全部走统一设置存储, 仅管理员可见。
export function SettingPlatform() {
    const t = useTranslations('setting');
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();

    const [markupRatio, setMarkupRatio] = useState('');
    const [minBalance, setMinBalance] = useState('');
    const [reserveCap, setReserveCap] = useState('');
    const [reserveImages, setReserveImages] = useState('');
    const [userRegister, setUserRegister] = useState(false);
    const [resellerRegister, setResellerRegister] = useState(false);
    const [approvalRequired, setApprovalRequired] = useState(true);

    // confirmed 记"服务端已确认落库的值", 是保存失败时唯一的回滚基准(理由见 settingSave.ts)。
    const confirmed = useRef<Record<string, string>>({});

    // 初始值只在设置列表首次就绪时对齐一次, 避免轮询刷新覆盖编辑中的草稿。
    const initialized = useRef(false);
    useEffect(() => {
        if (!settings || initialized.current) return;
        initialized.current = true;
        confirmed.current = confirmedFromSettings(settings);
        const find = (key: string) => settings.find((s) => s.key === key)?.value ?? '';
        setMarkupRatio(find(SettingKey.MarkupRatio) || '0.2');
        setMinBalance(find(SettingKey.MinBalance) || '0');
        setReserveCap(find(SettingKey.BalanceReserveOutputCap) || '4096');
        setReserveImages(find(SettingKey.BalanceReserveImages) || '4');
        setUserRegister(find(SettingKey.RegisterUserEnabled) === 'true');
        setResellerRegister(find(SettingKey.RegisterResellerEnabled) === 'true');
        setApprovalRequired(find(SettingKey.RegisterApprovalRequired) !== 'false');
    }, [settings]);

    // saveValue 提交单项; 成功记进基准, 失败回滚到基准值(理由见 settingSave.ts)。
    // apply 由调用方传入: 每个控件绑的是不同的 state, 统一在这里按 key 分发会变成一张易漏的映射表。
    //
    // 回滚逻辑在 settingSave.saveWithRollback 里(纯函数, 单测直接断言「失败时 apply 收到的是基准值」)。
    // 这里只负责把它的结果翻译成提示语: 真的还原了才说「已还原」。
    const saveValue = async (key: string, value: string, apply: (next: string) => void) => {
        const result = await saveWithRollback({
            key,
            value,
            confirmed: confirmed.current,
            currentInput: value,
            apply,
            submit: (k, v) => setSetting.mutateAsync({ key: k, value: v }),
        });
        if (result.ok) {
            toast.success(t('saved'));
            return;
        }
        toast.error(result.reverted ? t('saveFailedReverted') : result.message, { description: result.reverted ? result.message : undefined });
    };

    // saveNumber 数字项失焦: 先本地校验, 非负才提交。
    //
    // 校验失败**不回滚**: 此时请求根本没发出去, 服务端值与界面值本来就一致,
    // 强行改回基准值反而会把用户敲到一半的输入抹掉(比如刚输了个 "-" 想改成 "-1" 之外的值)。
    const saveNumber = (key: string, raw: string, apply: (next: string) => void) => {
        const value = Number(raw);
        if (!Number.isFinite(value) || value < 0) {
            toast.error(t('platform.invalidNumber'));
            return;
        }
        void saveValue(key, String(value), apply);
    };

    return (
        <section className="rounded-3xl border border-border bg-card text-card-foreground p-4">
            <h3 className="text-lg font-bold">{t('platform.title')}</h3>
            <Separator className="my-2" />
            <PlatformRow icon={<Percent className="size-4" />} label={t('platform.markupRatio')} hint={t('platform.markupRatioHint')}>
                <Input
                    type="number"
                    min={0}
                    step="any"
                    className="w-24"
                    value={markupRatio}
                    onChange={(e) => setMarkupRatio(e.target.value)}
                    onBlur={() => saveNumber(SettingKey.MarkupRatio, markupRatio, setMarkupRatio)}
                />
            </PlatformRow>
            <PlatformRow icon={<UserPlus className="size-4" />} label={t('platform.userRegister')} hint={t('platform.userRegisterHint')}>
                <Switch
                    checked={userRegister}
                    onCheckedChange={(checked) => {
                        setUserRegister(checked);
                        void saveValue(SettingKey.RegisterUserEnabled, String(checked), (next) => setUserRegister(next === 'true'));
                    }}
                />
            </PlatformRow>
            <PlatformRow icon={<Store className="size-4" />} label={t('platform.resellerRegister')} hint={t('platform.resellerRegisterHint')}>
                <Switch
                    checked={resellerRegister}
                    onCheckedChange={(checked) => {
                        setResellerRegister(checked);
                        void saveValue(SettingKey.RegisterResellerEnabled, String(checked), (next) => setResellerRegister(next === 'true'));
                    }}
                />
            </PlatformRow>
            <PlatformRow icon={<ShieldCheck className="size-4" />} label={t('platform.approvalRequired')} hint={t('platform.approvalRequiredHint')}>
                <Switch
                    checked={approvalRequired}
                    onCheckedChange={(checked) => {
                        setApprovalRequired(checked);
                        void saveValue(SettingKey.RegisterApprovalRequired, String(checked), (next) => setApprovalRequired(next === 'true'));
                    }}
                />
            </PlatformRow>
            <PlatformRow icon={<Wallet className="size-4" />} label={t('platform.minBalance')} hint={t('platform.minBalanceHint')}>
                <Input
                    type="number"
                    min={0}
                    step="any"
                    className="w-24"
                    value={minBalance}
                    onChange={(e) => setMinBalance(e.target.value)}
                    onBlur={() => saveNumber(SettingKey.MinBalance, minBalance, setMinBalance)}
                />
            </PlatformRow>
            <PlatformRow icon={<Gauge className="size-4" />} label={t('platform.reserveCap')} hint={t('platform.reserveCapHint')}>
                <Input
                    type="number"
                    min={0}
                    step={1}
                    className="w-24"
                    value={reserveCap}
                    onChange={(e) => setReserveCap(e.target.value)}
                    onBlur={() => saveNumber(SettingKey.BalanceReserveOutputCap, reserveCap, setReserveCap)}
                />
            </PlatformRow>
            <PlatformRow icon={<ImageIcon className="size-4" />} label={t('platform.reserveImages')} hint={t('platform.reserveImagesHint')}>
                <Input
                    type="number"
                    min={1}
                    step={1}
                    className="w-24"
                    value={reserveImages}
                    onChange={(e) => setReserveImages(e.target.value)}
                    onBlur={() => saveNumber(SettingKey.BalanceReserveImages, reserveImages, setReserveImages)}
                />
            </PlatformRow>
        </section>
    );
}
