import { useEffect, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Globe, Info, MessageSquare, Mail, Megaphone, Wrench, Power } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { Switch } from '@/components/ui/switch';
import { Separator } from '@/components/ui/separator';
import { useSettingList, useSetSetting, SettingKey } from '@/api/setting';
import { toast } from 'sonner';
import { confirmedFromSettings, saveWithRollback } from './settingSave';

// InfoRow 一行系统信息: 左侧图标与说明, 右侧控件; 控件由调用方决定。
function InfoRow({ icon, label, hint, children }: {
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

// SettingSystemInfo 系统信息配置: 站点名称、描述、联系方式、公告与维护模式。
// 全部落到统一的设置存储, 由公开端点 GET /api/v1/site/config 分发给登录页与已登录界面
// (浏览器标题、公告/维护横幅、密钥视图抬头); 仅管理员可见可改。保存成功后 useSetSetting
// 会广播失效站点配置缓存, 改动即时生效而无需刷新页面。
export function SettingSystemInfo() {
    const t = useTranslations('setting');
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();

    const [siteName, setSiteName] = useState('');
    const [siteDescription, setSiteDescription] = useState('');
    const [siteContact, setSiteContact] = useState('');
    const [announcement, setAnnouncement] = useState('');
    const [announcementEnabled, setAnnouncementEnabled] = useState(false);
    const [maintenanceMode, setMaintenanceMode] = useState(false);
    const [maintenanceNotice, setMaintenanceNotice] = useState('');

    // 初始值只在设置列表首次就绪时对齐一次, 避免轮询刷新覆盖正在编辑的草稿。
    const initialized = useRef(false);
    useEffect(() => {
        if (!settings || initialized.current) return;
        initialized.current = true;
        confirmed.current = confirmedFromSettings(settings);
        const find = (key: string) => settings.find((s) => s.key === key)?.value ?? '';
        setSiteName(find(SettingKey.SiteName));
        setSiteDescription(find(SettingKey.SiteDescription));
        setSiteContact(find(SettingKey.SiteContact));
        setAnnouncement(find(SettingKey.SiteAnnouncement));
        setAnnouncementEnabled(find(SettingKey.SiteAnnouncementEnabled) === 'true');
        setMaintenanceMode(find(SettingKey.MaintenanceMode) === 'true');
        setMaintenanceNotice(find(SettingKey.MaintenanceNotice));
    }, [settings]);

    // confirmed 记"服务端已确认落库的值", 它是回滚的唯一基准(理由见 settingSave.ts)。
    const confirmed = useRef<Record<string, string>>({});

    // saveValue 提交单项; 成功时把新值记进基准, 失败时回滚到基准值。
    //
    // 为什么必须回滚: 输入框是受控的, 显示的就是"刚敲进去的新值"。保存失败后若不回滚,
    // 界面停在那个值上而服务端仍是旧值 —— 管理员会按界面判断"站点名称已经改了",
    // 直到刷新页面才发现没有。apply 由调用方传入, 因为每个控件绑的不是同一个 state。
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

    return (
        <section className="rounded-3xl border border-border bg-card text-card-foreground p-4">
            <h3 className="text-lg font-bold">{t('systemInfo.title')}</h3>
            <Separator className="my-2" />
            <InfoRow icon={<Globe className="size-4" />} label={t('systemInfo.siteName')} hint={t('systemInfo.siteNameHint')}>
                <Input
                    className="w-44"
                    placeholder={t('systemInfo.siteNamePlaceholder')}
                    value={siteName}
                    onChange={(e) => setSiteName(e.target.value)}
                    onBlur={() => saveValue(SettingKey.SiteName, siteName, setSiteName)}
                />
            </InfoRow>
            <InfoRow icon={<Mail className="size-4" />} label={t('systemInfo.siteContact')} hint={t('systemInfo.siteContactHint')}>
                <Input
                    className="w-44"
                    placeholder={t('systemInfo.siteContactPlaceholder')}
                    value={siteContact}
                    onChange={(e) => setSiteContact(e.target.value)}
                    onBlur={() => saveValue(SettingKey.SiteContact, siteContact, setSiteContact)}
                />
            </InfoRow>
            <InfoRow icon={<Info className="size-4" />} label={t('systemInfo.siteDescription')} hint={t('systemInfo.siteDescriptionHint')}>
                <Textarea
                    className="w-56"
                    rows={2}
                    placeholder={t('systemInfo.siteDescriptionPlaceholder')}
                    value={siteDescription}
                    onChange={(e) => setSiteDescription(e.target.value)}
                    onBlur={() => saveValue(SettingKey.SiteDescription, siteDescription, setSiteDescription)}
                />
            </InfoRow>
            <Separator className="my-2" />
            <InfoRow icon={<Megaphone className="size-4" />} label={t('systemInfo.announcementEnabled')} hint={t('systemInfo.announcementEnabledHint')}>
                <Switch
                    checked={announcementEnabled}
                    onCheckedChange={(checked) => {
                        setAnnouncementEnabled(checked);
                        void saveValue(SettingKey.SiteAnnouncementEnabled, String(checked), (next) => setAnnouncementEnabled(next === 'true'));
                    }}
                />
            </InfoRow>
            <InfoRow icon={<MessageSquare className="size-4" />} label={t('systemInfo.announcement')} hint={t('systemInfo.announcementHint')}>
                <Textarea
                    className="w-56"
                    rows={2}
                    placeholder={t('systemInfo.announcementPlaceholder')}
                    value={announcement}
                    onChange={(e) => setAnnouncement(e.target.value)}
                    onBlur={() => saveValue(SettingKey.SiteAnnouncement, announcement, setAnnouncement)}
                />
            </InfoRow>
            <Separator className="my-2" />
            <InfoRow icon={<Power className="size-4" />} label={t('systemInfo.maintenanceMode')} hint={t('systemInfo.maintenanceModeHint')}>
                <Switch
                    checked={maintenanceMode}
                    onCheckedChange={(checked) => {
                        setMaintenanceMode(checked);
                        void saveValue(SettingKey.MaintenanceMode, String(checked), (next) => setMaintenanceMode(next === 'true'));
                    }}
                />
            </InfoRow>
            <InfoRow icon={<Wrench className="size-4" />} label={t('systemInfo.maintenanceNotice')} hint={t('systemInfo.maintenanceNoticeHint')}>
                <Textarea
                    className="w-56"
                    rows={2}
                    placeholder={t('systemInfo.maintenanceNoticePlaceholder')}
                    value={maintenanceNotice}
                    onChange={(e) => setMaintenanceNotice(e.target.value)}
                    onBlur={() => saveValue(SettingKey.MaintenanceNotice, maintenanceNotice, setMaintenanceNotice)}
                />
            </InfoRow>
        </section>
    );
}
