import { useEffect, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Percent, UserPlus, Store, ShieldCheck, Wallet, Gauge, Image as ImageIcon } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { Separator } from '@/components/ui/separator';
import { useSettingList, useSetSetting, SettingKey } from '@/api/setting';
import { toast } from 'sonner';

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

    // 初始值只在设置列表首次就绪时对齐一次, 避免轮询刷新覆盖编辑中的草稿。
    const initialized = useRef(false);
    useEffect(() => {
        if (!settings || initialized.current) return;
        initialized.current = true;
        const find = (key: string) => settings.find((s) => s.key === key)?.value ?? '';
        setMarkupRatio(find(SettingKey.MarkupRatio) || '0.2');
        setMinBalance(find(SettingKey.MinBalance) || '0');
        setReserveCap(find(SettingKey.BalanceReserveOutputCap) || '4096');
        setReserveImages(find(SettingKey.BalanceReserveImages) || '4');
        setUserRegister(find(SettingKey.RegisterUserEnabled) === 'true');
        setResellerRegister(find(SettingKey.RegisterResellerEnabled) === 'true');
        setApprovalRequired(find(SettingKey.RegisterApprovalRequired) !== 'false');
    }, [settings]);

    const saveValue = (key: string, value: string) => {
        setSetting.mutate({ key, value }, {
            onSuccess: () => toast.success(t('saved')),
            onError: (error) => toast.error(error.message),
        });
    };

    // 数字项失焦时校验非负再保存, 保留用户未完成的输入不强行回滚。
    const saveNumber = (key: string, raw: string) => {
        const value = Number(raw);
        if (!Number.isFinite(value) || value < 0) {
            toast.error(t('platform.invalidNumber'));
            return;
        }
        saveValue(key, String(value));
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
                    onBlur={() => saveNumber(SettingKey.MarkupRatio, markupRatio)}
                />
            </PlatformRow>
            <PlatformRow icon={<UserPlus className="size-4" />} label={t('platform.userRegister')} hint={t('platform.userRegisterHint')}>
                <Switch
                    checked={userRegister}
                    onCheckedChange={(checked) => {
                        setUserRegister(checked);
                        saveValue(SettingKey.RegisterUserEnabled, String(checked));
                    }}
                />
            </PlatformRow>
            <PlatformRow icon={<Store className="size-4" />} label={t('platform.resellerRegister')} hint={t('platform.resellerRegisterHint')}>
                <Switch
                    checked={resellerRegister}
                    onCheckedChange={(checked) => {
                        setResellerRegister(checked);
                        saveValue(SettingKey.RegisterResellerEnabled, String(checked));
                    }}
                />
            </PlatformRow>
            <PlatformRow icon={<ShieldCheck className="size-4" />} label={t('platform.approvalRequired')} hint={t('platform.approvalRequiredHint')}>
                <Switch
                    checked={approvalRequired}
                    onCheckedChange={(checked) => {
                        setApprovalRequired(checked);
                        saveValue(SettingKey.RegisterApprovalRequired, String(checked));
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
                    onBlur={() => saveNumber(SettingKey.MinBalance, minBalance)}
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
                    onBlur={() => saveNumber(SettingKey.BalanceReserveOutputCap, reserveCap)}
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
                    onBlur={() => saveNumber(SettingKey.BalanceReserveImages, reserveImages)}
                />
            </PlatformRow>
        </section>
    );
}
