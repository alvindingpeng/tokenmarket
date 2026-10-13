import { useTranslations } from 'use-intl';
import type { LucideIcon } from 'lucide-react';
import {
    ArrowRight,
    FolderTree,
    HeartPulse,
    Image as ImageIcon,
    KeyRound,
    Languages,
    MessageSquareText,
    Radio,
    ShieldCheck,
    Sparkles,
    Store,
    UserRound,
    Video,
    Wallet,
} from 'lucide-react';
import Logo from '@/components/modules/logo';
import { SiteBanners } from '@/components/site-banners';
import { Button } from '@/components/ui/button';
import { LanguageSwitch } from '@/components/language-switch';
import { DEFAULT_SITE_NAME, useSiteConfig, type SiteConfig } from '@/api/site';

// LandingHeader 顶部吸顶栏: 品牌 + 锚点导航 + 语言切换 + 控制台入口; 移动端只保留品牌、语言与按钮。
function LandingHeader({ siteName, onEnter }: { siteName: string; onEnter: () => void }) {
    const t = useTranslations('landing');
    const links = [
        { href: '#features', label: t('nav.features') },
        { href: '#media', label: t('nav.media') },
        { href: '#roles', label: t('nav.roles') },
    ];
    return (
        <header className="sticky top-0 z-50 border-b border-border/60 bg-background/80 backdrop-blur">
            <div className="mx-auto flex h-16 max-w-6xl items-center justify-between gap-3 px-4 md:px-6">
                <a href="#top" className="flex items-center gap-2.5">
                    <Logo size={34} />
                    <span className="text-lg font-bold tracking-tight">{siteName}</span>
                </a>
                <nav className="hidden items-center gap-1 md:flex" aria-label="Main">
                    {links.map((link) => (
                        <a
                            key={link.href}
                            href={link.href}
                            className="rounded-full px-3 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                        >
                            {link.label}
                        </a>
                    ))}
                </nav>
                <div className="flex items-center gap-2">
                    <LanguageSwitch />
                    <Button size="sm" className="rounded-full" onClick={onEnter}>
                        {t('hero.ctaPrimary')}
                    </Button>
                </div>
            </div>
        </header>
    );
}

// HeroSection 首屏: 产品主张 + 站点简介(来自系统信息配置, 未设置则回退默认文案) + 控制台入口。
function HeroSection({ onEnter }: { onEnter: () => void }) {
    const t = useTranslations('landing');
    const { data: site } = useSiteConfig();
    const chips = [
        { icon: Radio, text: t('hero.chip1') },
        { icon: MessageSquareText, text: t('hero.chip2') },
        { icon: Languages, text: t('hero.chip3') },
    ];
    const subtitle =
        site?.site_description?.trim() && site.site_description.trim() !== t('hero.subtitle')
            ? site.site_description.trim()
            : t('hero.subtitle');
    return (
        <section id="top" className="relative">
            <div className="mx-auto flex max-w-6xl flex-col items-center px-4 pt-16 pb-12 text-center md:px-6 md:pt-24 md:pb-16">
                <span className="inline-flex items-center gap-2 rounded-full border border-border bg-card px-3 py-1 text-xs font-medium text-muted-foreground">
                    <Sparkles className="size-3.5 text-primary" />
                    {t('hero.badge')}
                </span>
                <h1 className="mt-5 max-w-3xl text-3xl font-bold tracking-tight sm:text-4xl md:text-5xl">
                    <span className="bg-gradient-to-r from-primary to-primary/60 bg-clip-text text-transparent">
                        {t('hero.title')}
                    </span>
                </h1>
                <p className="mt-5 max-w-2xl text-base leading-relaxed text-muted-foreground md:text-lg">{subtitle}</p>
                <div className="mt-8 flex flex-col items-center gap-3 sm:flex-row">
                    <Button size="lg" className="rounded-full px-7" onClick={onEnter}>
                        {t('hero.ctaPrimary')}
                        <ArrowRight className="size-4" />
                    </Button>
                    <a
                        href="#features"
                        className="inline-flex h-10 items-center justify-center gap-2 rounded-full border bg-background px-6 text-sm font-medium shadow-xs transition-colors hover:bg-accent hover:text-accent-foreground dark:border-input dark:bg-input/30 dark:hover:bg-input/50"
                    >
                        {t('hero.ctaSecondary')}
                    </a>
                </div>
                <div className="mt-8 flex flex-wrap justify-center gap-2">
                    {chips.map((chip) => (
                        <span
                            key={chip.text}
                            className="inline-flex items-center gap-1.5 rounded-full border border-border bg-card px-3 py-1.5 text-xs text-muted-foreground"
                        >
                            <chip.icon className="size-3.5 text-primary" />
                            {chip.text}
                        </span>
                    ))}
                </div>
                {/* 维护模式对访客同样可见: 被维护挡住的人需要知道登不上的原因。 */}
                <SiteBanners className="mt-6" includeAnnouncement={false} />
            </div>
        </section>
    );
}

// FeaturesSection 全站功能一览: 网关/渠道/选路/计费/媒体/运维/访问/白标 八张卡, 与后台模块一一对应。
function FeaturesSection() {
    const t = useTranslations('landing');
    const features: { icon: LucideIcon; title: string; desc: string }[] = [
        { icon: Radio, title: t('features.gatewayTitle'), desc: t('features.gatewayDesc') },
        { icon: KeyRound, title: t('features.channelsTitle'), desc: t('features.channelsDesc') },
        { icon: FolderTree, title: t('features.groupsTitle'), desc: t('features.groupsDesc') },
        { icon: Wallet, title: t('features.billingTitle'), desc: t('features.billingDesc') },
        { icon: Video, title: t('features.mediaTitle'), desc: t('features.mediaDesc') },
        { icon: HeartPulse, title: t('features.opsTitle'), desc: t('features.opsDesc') },
        { icon: UserRound, title: t('features.accessTitle'), desc: t('features.accessDesc') },
        { icon: Sparkles, title: t('features.whiteLabelTitle'), desc: t('features.whiteLabelDesc') },
    ];
    return (
        <section id="features" className="scroll-mt-16 border-t border-border/60">
            <div className="mx-auto max-w-6xl px-4 py-16 md:px-6 md:py-24">
                <div className="mx-auto max-w-2xl text-center">
                    <h2 className="text-2xl font-bold tracking-tight md:text-3xl">{t('features.title')}</h2>
                    <p className="mt-3 text-muted-foreground">{t('features.subtitle')}</p>
                </div>
                <div className="mt-10 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
                    {features.map((feature) => (
                        <div
                            key={feature.title}
                            className="rounded-3xl border border-border bg-card p-6 transition-colors hover:border-primary/40 hover:bg-accent/40"
                        >
                            <div className="flex size-10 items-center justify-center rounded-2xl bg-primary/10 text-primary [&>svg]:size-5">
                                <feature.icon />
                            </div>
                            <h3 className="mt-4 font-semibold">{feature.title}</h3>
                            <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{feature.desc}</p>
                        </div>
                    ))}
                </div>
            </div>
        </section>
    );
}

// MediaSection 接入协议与计费方式: 入站协议列表 + 文本/图像/视频三种形态各自的计费口径。
function MediaSection() {
    const t = useTranslations('landing');
    const kinds: { icon: LucideIcon; title: string; desc: string; price: string }[] = [
        { icon: MessageSquareText, title: t('media.textTitle'), desc: t('media.textDesc'), price: t('media.textPrice') },
        { icon: ImageIcon, title: t('media.imageTitle'), desc: t('media.imageDesc'), price: t('media.imagePrice') },
        { icon: Video, title: t('media.videoTitle'), desc: t('media.videoDesc'), price: t('media.videoPrice') },
    ];
    return (
        <section id="media" className="scroll-mt-16 border-t border-border/60 bg-accent/30">
            <div className="mx-auto max-w-6xl px-4 py-16 md:px-6 md:py-24">
                <div className="mx-auto max-w-2xl text-center">
                    <h2 className="text-2xl font-bold tracking-tight md:text-3xl">{t('media.title')}</h2>
                    <p className="mt-3 text-muted-foreground">{t('media.subtitle')}</p>
                </div>
                <div className="mt-10 rounded-3xl border border-border bg-card p-6 text-center">
                    <span className="text-xs font-semibold uppercase tracking-wider text-primary">{t('media.protocolsTitle')}</span>
                    <p className="mx-auto mt-2 max-w-3xl text-sm leading-relaxed text-muted-foreground">{t('media.protocolsDesc')}</p>
                </div>
                <div className="mt-4 grid gap-4 md:grid-cols-3">
                    {kinds.map((kind) => (
                        <div key={kind.title} className="rounded-3xl border border-border bg-card p-6">
                            <div className="flex size-10 items-center justify-center rounded-2xl bg-primary/10 text-primary [&>svg]:size-5">
                                <kind.icon />
                            </div>
                            <h3 className="mt-4 font-semibold">{kind.title}</h3>
                            <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{kind.desc}</p>
                            <p className="mt-4 inline-flex items-center gap-1.5 rounded-xl bg-muted/70 px-3 py-2 text-xs font-medium text-foreground">
                                <Wallet className="size-3.5 text-primary" />
                                {kind.price}
                            </p>
                        </div>
                    ))}
                </div>
            </div>
        </section>
    );
}

// RolesSection 三种角色: 导航按角色裁剪, 界面只出现有权使用的功能。
function RolesSection() {
    const t = useTranslations('landing');
    const roles: { icon: LucideIcon; title: string; desc: string }[] = [
        { icon: ShieldCheck, title: t('roles.adminTitle'), desc: t('roles.adminDesc') },
        { icon: Store, title: t('roles.resellerTitle'), desc: t('roles.resellerDesc') },
        { icon: UserRound, title: t('roles.userTitle'), desc: t('roles.userDesc') },
    ];
    return (
        <section id="roles" className="scroll-mt-16 border-t border-border/60">
            <div className="mx-auto max-w-6xl px-4 py-16 md:px-6 md:py-24">
                <div className="mx-auto max-w-2xl text-center">
                    <h2 className="text-2xl font-bold tracking-tight md:text-3xl">{t('roles.title')}</h2>
                    <p className="mt-3 text-muted-foreground">{t('roles.subtitle')}</p>
                </div>
                <div className="mt-10 grid gap-4 md:grid-cols-3">
                    {roles.map((role) => (
                        <div key={role.title} className="rounded-3xl border border-border bg-card p-6">
                            <div className="flex size-10 items-center justify-center rounded-2xl bg-primary/10 text-primary [&>svg]:size-5">
                                <role.icon />
                            </div>
                            <h3 className="mt-4 font-semibold">{role.title}</h3>
                            <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{role.desc}</p>
                        </div>
                    ))}
                </div>
            </div>
        </section>
    );
}

// CtaSection 底部行动区: 统一的控制台入口。
function CtaSection({ onEnter }: { onEnter: () => void }) {
    const t = useTranslations('landing');
    return (
        <section className="border-t border-border/60">
            <div className="mx-auto max-w-6xl px-4 py-16 md:px-6 md:py-20">
                <div className="relative overflow-hidden rounded-3xl border border-border bg-card px-6 py-14 text-center md:py-16">
                    <div className="mx-auto max-w-xl">
                        <h2 className="text-2xl font-bold tracking-tight md:text-3xl">{t('cta.title')}</h2>
                        <p className="mt-3 text-muted-foreground">{t('cta.subtitle')}</p>
                        <div className="mt-8 flex justify-center">
                            <Button size="lg" className="rounded-full px-8" onClick={onEnter}>
                                {t('cta.button')}
                                <ArrowRight className="size-4" />
                            </Button>
                        </div>
                    </div>
                </div>
            </div>
        </section>
    );
}

// LandingFooter 页脚: 品牌 + 标语 + 站点联系方式(系统信息配置)。
function LandingFooter({ siteName, site }: { siteName: string; site: SiteConfig | undefined }) {
    const t = useTranslations('landing');
    const contact = site?.site_contact?.trim();
    const href =
        contact && /^https?:\/\//i.test(contact)
            ? contact
            : contact && /^[\w.+-]+@[\w-]+\.[\w.-]+$/.test(contact)
              ? 'mailto:' + contact
              : undefined;
    return (
        <footer className="border-t border-border/60">
            <div className="mx-auto flex max-w-6xl flex-col items-center gap-4 px-4 py-10 text-center md:px-6">
                <div className="flex items-center gap-2.5">
                    <Logo size={28} />
                    <span className="font-semibold">{siteName}</span>
                </div>
                <p className="text-sm text-muted-foreground">{t('footer.tagline')}</p>
                {contact &&
                    (href ? (
                        <a className="text-sm text-muted-foreground underline underline-offset-2 hover:text-foreground" href={href}>
                            {t('footer.contact')}
                        </a>
                    ) : (
                        <span className="text-sm text-muted-foreground">{contact}</span>
                    ))}
                <div className="text-xs text-muted-foreground/70">
                    {new Date().getFullYear()} · {siteName}
                </div>
            </div>
        </footer>
    );
}

// LandingPage 落地页: 未登录访客的首页, 罗列全站功能并将控制台入口导向登录。
export function LandingPage({ onEnter }: { onEnter: () => void }) {
    const { data: site } = useSiteConfig();
    const siteName = site?.site_name?.trim() || DEFAULT_SITE_NAME;
    return (
        <div className="min-h-dvh scroll-smooth bg-background text-foreground">
            <LandingHeader siteName={siteName} onEnter={onEnter} />
            <main>
                <HeroSection onEnter={onEnter} />
                <FeaturesSection />
                <MediaSection />
                <RolesSection />
                <CtaSection onEnter={onEnter} />
            </main>
            <LandingFooter siteName={siteName} site={site} />
        </div>
    );
}
