import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { Megaphone, Wrench, X } from 'lucide-react';
import { useSiteConfig } from '@/api/site';

// DISMISS_KEY 记住已被用户关闭的那条公告: 按正文比对, 管理员改写公告后会自动重新出现。
const DISMISS_KEY = 'octopus-announcement-dismissed';

// AnnouncementBanner 全局公告条: 仅当后端下发非空正文时渲染(开关过滤已在后端做完)。
function AnnouncementBanner({ text }: { text: string }) {
    const t = useTranslations('site');
    const [dismissed, setDismissed] = useState(() => {
        try {
            return sessionStorage.getItem(DISMISS_KEY) === text;
        } catch {
            return false;
        }
    });

    if (dismissed) return null;

    return (
        <div className="flex items-start gap-2 rounded-2xl border border-primary/30 bg-primary/10 px-3 py-2 text-sm">
            <Megaphone className="mt-0.5 size-4 shrink-0 text-primary" />
            <div className="min-w-0 flex-1">
                <span className="font-medium">{t('announcement')}</span>
                <span className="mx-2 text-muted-foreground">·</span>
                {/* 公告是管理员自由输入的纯文本: 原样渲染不解析 HTML, 保留换行。 */}
                <span className="whitespace-pre-wrap break-words text-foreground">{text}</span>
            </div>
            <button
                type="button"
                aria-label={t('dismiss')}
                className="shrink-0 rounded-lg p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                onClick={() => {
                    try {
                        sessionStorage.setItem(DISMISS_KEY, text);
                    } catch {
                        // 隐私模式下 sessionStorage 会抛错: 忽略, 本次渲染内仍然隐藏。
                    }
                    setDismissed(true);
                }}
            >
                <X className="size-3.5" />
            </button>
        </div>
    );
}

// MaintenanceBanner 维护模式提示: 常驻不可关闭 —— 管理员必须看得见自己开着这个开关,
// 否则"用户说登不上而管理员觉得一切正常"会是最难排查的一类工单。
function MaintenanceBanner({ notice }: { notice: string }) {
    const t = useTranslations('site');
    return (
        <div className="flex items-start gap-2 rounded-2xl border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm">
            <Wrench className="mt-0.5 size-4 shrink-0 text-destructive" />
            <div className="min-w-0 flex-1">
                <span className="font-medium text-destructive">{t('maintenance')}</span>
                <span className="mx-2 text-muted-foreground">·</span>
                <span className="whitespace-pre-wrap break-words text-foreground">{notice}</span>
            </div>
        </div>
    );
}

// SiteBanners 渲染「系统信息配置」的两条全局横幅: 维护模式提示与公告。
// 登录页与应用外壳共用同一份站点配置缓存, 管理员保存后两侧同帧更新。
// includeAnnouncement=false 用于未登录场景: 公告按管理员说明只面向登录用户,
// 而维护提示必须对访客可见 —— 被维护模式挡在门外的人需要知道为什么登不进来。
export function SiteBanners({ className, includeAnnouncement = true }: {
    className?: string;
    includeAnnouncement?: boolean;
}) {
    const { data: site } = useSiteConfig();
    if (!site) return null;

    const announcement = includeAnnouncement ? (site.announcement?.trim() ?? '') : '';
    const notice = site.maintenance_notice?.trim() || 'service under maintenance';

    if (!site.maintenance_mode && announcement.length === 0) return null;

    return (
        <div className={className ?? 'flex flex-none flex-col gap-2 px-2 md:px-0'}>
            {site.maintenance_mode && <MaintenanceBanner notice={notice} />}
            {announcement.length > 0 && <AnnouncementBanner text={announcement} />}
        </div>
    );
}
