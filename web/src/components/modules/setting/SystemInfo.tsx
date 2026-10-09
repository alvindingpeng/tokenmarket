import { useEffect, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Globe, Info, MessageSquare, Mail, Megaphone, Wrench, Power } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { Switch } from '@/components/ui/switch';
import { Separator } from '@/components/ui/separator';
import { useSettingList, useSetSetting, SettingKey } from '@/api/setting';
import { toast } from 'sonner';

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
// 全部落到统一的设置存储, 由公开端点分发给未登录的登录页使用; 仅管理员可见可改。
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
        const find = (key: string) => settings.find((s) => s.key === key)?.value ?? '';
        setSiteName(find(SettingKey.SiteName));
        setSiteDescription(find(SettingKey.SiteDescription));
        setSiteContact(find(SettingKey.SiteContact));
        setAnnouncement(find(SettingKey.SiteAnnouncement));
        setAnnouncementEnabled(find(SettingKey.SiteAnnouncementEnabled) === 'true');
        setMaintenanceMode(find(SettingKey.MaintenanceMode) === 'true');
        setMaintenanceNotice(find(SettingKey.MaintenanceNotice));
    }, [settings]);

    const saveValue = (key: string, value: string) => {
        setSetting.mutate({ key, value }, {
            onSuccess: () => toast.success(t('saved')),
            onError: (error) => toast.error(error.message),
        });
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
                    onBlur={() => saveValue(SettingKey.SiteName, siteName)}
                />
            </InfoRow>
            <InfoRow icon={<Mail className="size-4" />} label={t('systemInfo.siteContact')} hint={t('systemInfo.siteContactHint')}>
                <Input
                    className="w-44"
                    placeholder={t('systemInfo.siteContactPlaceholder')}
                    value={siteContact}
                    onChange={(e) => setSiteContact(e.target.value)}
                    onBlur={() => saveValue(SettingKey.SiteContact, siteContact)}
                />
            </InfoRow>
            <InfoRow icon={<Info className="size-4" />} label={t('systemInfo.siteDescription')} hint={t('systemInfo.siteDescriptionHint')}>
                <Textarea
                    className="w-56"
                    rows={2}
                    placeholder={t('systemInfo.siteDescriptionPlaceholder')}
                    value={siteDescription}
                    onChange={(e) => setSiteDescription(e.target.value)}
                    onBlur={() => saveValue(SettingKey.SiteDescription, siteDescription)}
                />
            </InfoRow>
            <Separator className="my-2" />
            <InfoRow icon={<Megaphone className="size-4" />} label={t('systemInfo.announcementEnabled')} hint={t('systemInfo.announcementEnabledHint')}>
                <Switch
                    checked={announcementEnabled}
                    onCheckedChange={(checked) => {
                        setAnnouncementEnabled(checked);
                        saveValue(SettingKey.SiteAnnouncementEnabled, String(checked));
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
                    onBlur={() => saveValue(SettingKey.SiteAnnouncement, announcement)}
                />
            </InfoRow>
            <Separator className="my-2" />
            <InfoRow icon={<Power className="size-4" />} label={t('systemInfo.maintenanceMode')} hint={t('systemInfo.maintenanceModeHint')}>
                <Switch
                    checked={maintenanceMode}
                    onCheckedChange={(checked) => {
                        setMaintenanceMode(checked);
                        saveValue(SettingKey.MaintenanceMode, String(checked));
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
                    onBlur={() => saveValue(SettingKey.MaintenanceNotice, maintenanceNotice)}
                />
            </InfoRow>
        </section>
    );
}
