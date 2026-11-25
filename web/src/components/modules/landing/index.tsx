import { useState } from 'react';
import { useTranslations } from 'use-intl';
import type { LucideIcon } from 'lucide-react';
import {
    Activity,
    ArrowRight,
    ArrowUpRight,
    Cable,
    Check,
    FolderTree,
    GitMerge,
    HeartPulse,
    Image as ImageIcon,
    KeyRound,
    Languages,
    MessageSquareText,
    Radio,
    Receipt,
    Send,
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

type WorkbenchScenarioID = 'coding' | 'summary' | 'media';

const SCENARIO_IDS: WorkbenchScenarioID[] = ['coding', 'summary', 'media'];

// LandingHeader 顶部吸顶栏: 品牌 + 锚点导航 + 语言切换 + 控制台入口; 移动端保留品牌、实时状态、语言与按钮。
function LandingHeader({ siteName, onEnter }: { siteName: string; onEnter: () => void }) {
    const t = useTranslations('landing');
    const links = [
        { href: '/status', label: t('nav.status') },
        { href: '#flow', label: t('nav.flow') },
        { href: '#features', label: t('nav.features') },
        { href: '#media', label: t('nav.media') },
        { href: '#roles', label: t('nav.roles') },
    ];
    return (
        <header className="sticky top-0 z-50 border-b border-slate-900/10 bg-[#f6f8fb]/88 backdrop-blur-md dark:border-white/10 dark:bg-[#0b1220]/88">
            <div className="mx-auto flex min-h-16 max-w-6xl flex-wrap items-center justify-between gap-3 px-4 py-2.5 md:flex-nowrap md:px-6 md:py-0">
                <a href="#top" className="flex items-center gap-2.5 text-slate-900 dark:text-slate-100">
                    <Logo size={32} />
                    <span className="text-base font-semibold tracking-tight md:text-lg">{siteName}</span>
                </a>
                <nav className="hidden items-center gap-1 md:flex" aria-label="Main">
                    {links.map((link) => (
                        <a
                            key={link.href}
                            href={link.href}
                            className="rounded-lg px-3 py-1.5 text-sm text-slate-600 transition-colors hover:bg-slate-900/5 hover:text-slate-950 dark:text-slate-300 dark:hover:bg-white/8 dark:hover:text-white"
                        >
                            {link.label}
                        </a>
                    ))}
                </nav>
                <div className="flex w-full items-center justify-between gap-2 sm:w-auto sm:justify-end">
                    <a
                        href="/status"
                        className="inline-flex min-h-10 items-center text-sm font-medium text-slate-700 hover:text-slate-950 dark:text-slate-200 dark:hover:text-white md:hidden"
                    >
                        {t('nav.status')}
                    </a>
                    <div className="flex items-center gap-2">
                        <LanguageSwitch />
                        <Button
                            size="sm"
                            className="rounded-lg bg-[#1d4ed8] px-4 text-white hover:bg-[#1e40af] dark:bg-[#3b82f6] dark:text-slate-950 dark:hover:bg-[#60a5fa]"
                            onClick={onEnter}
                        >
                            {t('hero.ctaPrimary')}
                        </Button>
                    </div>
                </div>
            </div>
        </header>
    );
}

// SmartRoutingWorkbench 首屏交互式编组调度台: 访客可切换真实调用场景，直观查看协议入站、智能路由评分、热备切道与预扣结算。
function SmartRoutingWorkbench() {
    const t = useTranslations('landing');
    const [active, setActive] = useState<WorkbenchScenarioID>('coding');
    const prefix = `workbench.scenarios.${active}`;

    return (
        <div className="overflow-hidden rounded-2xl border border-slate-900/12 bg-white shadow-[0_24px_60px_-28px_rgba(15,23,42,0.22)] dark:border-white/12 dark:bg-[#111b2e] dark:shadow-[0_28px_70px_-28px_rgba(0,0,0,0.75)]">
            <div className="flex flex-wrap items-center justify-between gap-2 border-b border-slate-900/8 px-4 py-3.5 dark:border-white/10 sm:px-5">
                <div className="flex items-center gap-2 text-sm font-semibold text-slate-900 dark:text-slate-100">
                    <GitMerge className="size-4 text-[#1d4ed8] dark:text-[#60a5fa]" />
                    <span>{t('workbench.heading')}</span>
                </div>
                <span className="inline-flex items-center gap-1.5 text-xs font-medium text-emerald-700 dark:text-emerald-400">
                    <span className="size-2 rounded-full bg-emerald-500" aria-hidden="true" />
                    {t('workbench.statusLive')}
                </span>
            </div>

            <div
                role="tablist"
                aria-label={t('workbench.heading')}
                className="grid grid-cols-3 border-b border-slate-900/8 bg-slate-50/80 p-1.5 dark:border-white/10 dark:bg-slate-950/35"
            >
                {SCENARIO_IDS.map((id) => {
                    const selected = active === id;
                    return (
                        <button
                            key={id}
                            type="button"
                            role="tab"
                            aria-selected={selected}
                            onClick={() => setActive(id)}
                            className={
                                selected
                                    ? 'rounded-lg bg-white px-2.5 py-2 text-xs font-semibold text-slate-950 shadow-xs transition-colors dark:bg-[#1b2944] dark:text-white'
                                    : 'rounded-lg px-2.5 py-2 text-xs font-medium text-slate-600 transition-colors hover:text-slate-950 dark:text-slate-400 dark:hover:text-slate-200'
                            }
                        >
                            {t(`workbench.tabs.${id}`)}
                        </button>
                    );
                })}
            </div>

            <div className="divide-y divide-slate-900/8 dark:divide-white/10">
                <div className="px-4 py-4 sm:px-5">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                        <span className="text-xs font-medium text-slate-500 dark:text-slate-400">
                            {t('workbench.inboundLabel')}
                        </span>
                        <code className="rounded bg-slate-100 px-2 py-0.5 font-mono text-xs text-slate-800 dark:bg-white/8 dark:text-slate-200">
                            {t(`${prefix}.endpoint`)}
                        </code>
                    </div>
                    <div className="mt-2 flex flex-wrap items-baseline justify-between gap-2">
                        <span className="text-sm font-semibold text-slate-950 dark:text-white">
                            {t(`${prefix}.protocol`)}
                        </span>
                        <span className="font-mono text-xs text-[#1d4ed8] dark:text-[#60a5fa]">
                            {t(`${prefix}.model`)}
                        </span>
                    </div>
                </div>

                <div className="px-4 py-4 sm:px-5">
                    <div className="text-xs font-medium text-slate-500 dark:text-slate-400">
                        {t('workbench.routingLabel')}
                    </div>
                    <p className="mt-1.5 text-sm leading-relaxed text-slate-700 dark:text-slate-200">
                        {t(`${prefix}.policy`)}
                    </p>
                </div>

                <div className="space-y-2.5 px-4 py-4 sm:px-5">
                    <div className="text-xs font-medium text-slate-500 dark:text-slate-400">
                        {t('workbench.tracksLabel')}
                    </div>
                    <div className="flex items-start gap-2.5 rounded-xl border border-[#1d4ed8]/25 bg-[#1d4ed8]/[0.05] px-3 py-2.5 dark:border-[#60a5fa]/30 dark:bg-[#3b82f6]/10">
                        <span className="mt-0.5 shrink-0 rounded bg-[#1d4ed8] px-1.5 py-0.5 text-[11px] font-medium text-white dark:bg-[#3b82f6] dark:text-slate-950">
                            {t('workbench.primaryBadge')}
                        </span>
                        <span className="text-xs leading-relaxed text-slate-800 dark:text-slate-100">
                            {t(`${prefix}.trackA`)}
                        </span>
                    </div>
                    <div className="flex items-start gap-2.5 rounded-xl border border-slate-900/8 bg-slate-50/80 px-3 py-2.5 dark:border-white/8 dark:bg-white/[0.03]">
                        <span className="mt-0.5 shrink-0 rounded bg-slate-200 px-1.5 py-0.5 text-[11px] font-medium text-slate-700 dark:bg-white/12 dark:text-slate-300">
                            {t('workbench.standbyBadge')}
                        </span>
                        <span className="text-xs leading-relaxed text-slate-600 dark:text-slate-300">
                            {t(`${prefix}.trackB`)}
                        </span>
                    </div>
                </div>

                <div className="bg-slate-50/70 px-4 py-3.5 dark:bg-slate-950/30 sm:px-5">
                    <div className="text-xs font-medium text-slate-500 dark:text-slate-400">
                        {t('workbench.ledgerLabel')}
                    </div>
                    <p className="mt-1 text-xs leading-relaxed text-slate-700 dark:text-slate-300">
                        {t(`${prefix}.ledger`)}
                    </p>
                </div>
            </div>
        </div>
    );
}

// HeroSection 首屏: 左侧大字主张与操作入口，右侧可交互智能编组调度台。
function HeroSection({ onEnter }: { onEnter: () => void }) {
    const t = useTranslations('landing');
    const { data: site } = useSiteConfig();
    const subtitle =
        site?.site_description?.trim() && site.site_description.trim() !== t('hero.subtitle')
            ? site.site_description.trim()
            : t('hero.subtitle');

    return (
        <section id="top" className="relative overflow-x-clip">
            <div
                aria-hidden="true"
                className="pointer-events-none absolute inset-x-0 top-0 h-[34rem] bg-[radial-gradient(ellipse_65%_55%_at_75%_15%,rgba(29,78,216,0.10),transparent_70%)] dark:bg-[radial-gradient(ellipse_65%_55%_at_75%_15%,rgba(59,130,246,0.16),transparent_70%)]"
            />
            <div className="relative mx-auto grid max-w-6xl gap-10 px-4 pt-12 pb-14 md:px-6 md:pt-20 md:pb-20 lg:grid-cols-12 lg:items-center lg:gap-12">
                <div className="lg:col-span-6">
                    <h1 className="max-w-xl text-3xl font-semibold tracking-[-0.03em] text-slate-950 sm:text-4xl lg:text-[3.1rem] lg:leading-[1.12] dark:text-white">
                        {t('hero.title')}
                    </h1>
                    <p className="mt-5 max-w-xl text-base leading-relaxed text-slate-600 md:text-lg dark:text-slate-300">
                        {subtitle}
                    </p>
                    <div className="mt-8 flex flex-wrap items-center gap-3">
                        <Button
                            size="lg"
                            className="h-11 rounded-lg bg-[#1d4ed8] px-6 text-sm font-medium text-white shadow-sm hover:bg-[#1e40af] dark:bg-[#3b82f6] dark:text-slate-950 dark:hover:bg-[#60a5fa]"
                            onClick={onEnter}
                        >
                            {t('hero.ctaPrimary')}
                            <ArrowRight className="size-4" />
                        </Button>
                        <a
                            href="/status"
                            className="inline-flex h-11 items-center justify-center gap-2 rounded-lg border border-slate-900/15 bg-white px-5 text-sm font-medium text-slate-800 transition-colors hover:bg-slate-50 dark:border-white/15 dark:bg-white/5 dark:text-slate-100 dark:hover:bg-white/10"
                        >
                            <Activity className="size-4 text-emerald-600 dark:text-emerald-400" />
                            {t('cta.statusButton')}
                        </a>
                    </div>
                    {/* 维护模式对访客同样可见: 被维护挡住的人需要知道登不上的原因。 */}
                    <SiteBanners className="mt-6" includeAnnouncement={false} />
                </div>
                <div className="lg:col-span-6">
                    <SmartRoutingWorkbench />
                </div>
            </div>
        </section>
    );
}

// MetricsBand 架构规格带: 四列细线分隔呈现平台核心边界。
function MetricsBand() {
    const t = useTranslations('landing');
    const items = [
        { value: t('metrics.m1Value'), label: t('metrics.m1Label') },
        { value: t('metrics.m2Value'), label: t('metrics.m2Label') },
        { value: t('metrics.m3Value'), label: t('metrics.m3Label') },
        { value: t('metrics.m4Value'), label: t('metrics.m4Label') },
    ];
    return (
        <section className="border-y border-slate-900/10 bg-white dark:border-white/10 dark:bg-[#0f172a]">
            <div className="mx-auto grid max-w-6xl grid-cols-2 divide-y divide-slate-900/8 px-4 dark:divide-white/10 sm:divide-y-0 lg:grid-cols-4 lg:divide-x md:px-6">
                {items.map((item) => (
                    <div key={item.label} className="px-3 py-6 sm:px-5 lg:py-7">
                        <div className="text-xl font-semibold tracking-tight text-slate-950 tabular-nums dark:text-white md:text-2xl">
                            {item.value}
                        </div>
                        <div className="mt-1 text-xs leading-relaxed text-slate-600 dark:text-slate-400 sm:text-sm">
                            {item.label}
                        </div>
                    </div>
                ))}
            </div>
        </section>
    );
}

// FlowSection 编组流程: 汇入 → 编组 → 发车 → 计量, 把品牌隐喻和实际数据流对上, 让访客一眼看懂钱和请求各自经过哪一段。
function FlowSection() {
    const t = useTranslations('landing');
    const steps: { icon: LucideIcon; title: string; desc: string }[] = [
        { icon: Cable, title: t('flow.step1Title'), desc: t('flow.step1Desc') },
        { icon: GitMerge, title: t('flow.step2Title'), desc: t('flow.step2Desc') },
        { icon: Send, title: t('flow.step3Title'), desc: t('flow.step3Desc') },
        { icon: Receipt, title: t('flow.step4Title'), desc: t('flow.step4Desc') },
    ];
    return (
        <section id="flow" className="scroll-mt-16">
            <div className="mx-auto max-w-6xl px-4 py-16 md:px-6 md:py-22">
                <div className="max-w-2xl">
                    <h2 className="text-2xl font-semibold tracking-tight text-slate-950 md:text-3xl dark:text-white">
                        {t('flow.title')}
                    </h2>
                    <p className="mt-3 text-base leading-relaxed text-slate-600 dark:text-slate-300">
                        {t('flow.subtitle')}
                    </p>
                </div>
                <ol className="relative mt-10 grid gap-6 sm:grid-cols-2 lg:grid-cols-4">
                    {steps.map((step, index) => (
                        <li
                            key={step.title}
                            className="relative flex flex-col border-t-2 border-slate-900/12 pt-5 dark:border-white/15"
                        >
                            <div className="flex items-center justify-between">
                                <span className="font-mono text-xs font-semibold text-[#1d4ed8] tabular-nums dark:text-[#60a5fa]">
                                    {String(index + 1).padStart(2, '0')}
                                </span>
                                <step.icon className="size-4 text-slate-400 dark:text-slate-500" aria-hidden="true" />
                            </div>
                            <h3 className="mt-4 text-lg font-semibold text-slate-950 dark:text-white">{step.title}</h3>
                            <p className="mt-2 text-sm leading-relaxed text-slate-600 dark:text-slate-300">{step.desc}</p>
                        </li>
                    ))}
                </ol>
            </div>
        </section>
    );
}

// FeaturesSection 核心能力矩阵: 顶部两块重点能力主面板 + 底部四列细线模块，区分主次层级。
function FeaturesSection() {
    const t = useTranslations('landing');
    const routingTags = ['t1', 't2', 't3', 't4', 't5'].map((key) => t(`highlights.smartRoutingTags.${key}`));
    const marketTags = ['t1', 't2', 't3', 't4', 't5'].map((key) => t(`highlights.marketTags.${key}`));

    const pillars: { icon: LucideIcon; title: string; desc: string }[] = [
        { icon: KeyRound, title: t('features.channelsTitle'), desc: t('highlights.channelsDetail') },
        { icon: Wallet, title: t('features.billingTitle'), desc: t('highlights.billingDetail') },
        { icon: HeartPulse, title: t('features.opsTitle'), desc: t('highlights.opsDetail') },
        { icon: Sparkles, title: t('features.accessTitle'), desc: t('highlights.accessDetail') },
    ];

    return (
        <section id="features" className="scroll-mt-16 border-t border-slate-900/10 bg-white dark:border-white/10 dark:bg-[#0f172a]">
            <div className="mx-auto max-w-6xl px-4 py-16 md:px-6 md:py-22">
                <div className="max-w-2xl">
                    <h2 className="text-2xl font-semibold tracking-tight text-slate-950 md:text-3xl dark:text-white">
                        {t('features.title')}
                    </h2>
                    <p className="mt-3 text-base leading-relaxed text-slate-600 dark:text-slate-300">
                        {t('features.subtitle')}
                    </p>
                </div>

                <div className="mt-10 grid gap-5 lg:grid-cols-2">
                    <article className="flex flex-col justify-between rounded-2xl border border-slate-900/10 bg-[#f6f8fb] p-6 sm:p-8 dark:border-white/10 dark:bg-[#111b2e]">
                        <div>
                            <div className="inline-flex size-10 items-center justify-center rounded-xl bg-[#1d4ed8]/10 text-[#1d4ed8] dark:bg-[#3b82f6]/15 dark:text-[#60a5fa]">
                                <FolderTree className="size-5" />
                            </div>
                            <h3 className="mt-5 text-xl font-semibold tracking-tight text-slate-950 dark:text-white">
                                {t('highlights.smartRoutingTitle')}
                            </h3>
                            <p className="mt-3 text-sm leading-relaxed text-slate-600 dark:text-slate-300">
                                {t('highlights.smartRoutingDesc')}
                            </p>
                        </div>
                        <div className="mt-6 flex flex-wrap gap-2">
                            {routingTags.map((tag) => (
                                <span
                                    key={tag}
                                    className="rounded-md border border-slate-900/10 bg-white px-2.5 py-1 text-xs font-medium text-slate-700 dark:border-white/10 dark:bg-white/5 dark:text-slate-200"
                                >
                                    {tag}
                                </span>
                            ))}
                        </div>
                    </article>

                    <article className="flex flex-col justify-between rounded-2xl border border-slate-900/10 bg-[#f6f8fb] p-6 sm:p-8 dark:border-white/10 dark:bg-[#111b2e]">
                        <div>
                            <div className="inline-flex size-10 items-center justify-center rounded-xl bg-[#1d4ed8]/10 text-[#1d4ed8] dark:bg-[#3b82f6]/15 dark:text-[#60a5fa]">
                                <Store className="size-5" />
                            </div>
                            <h3 className="mt-5 text-xl font-semibold tracking-tight text-slate-950 dark:text-white">
                                {t('highlights.marketTitle')}
                            </h3>
                            <p className="mt-3 text-sm leading-relaxed text-slate-600 dark:text-slate-300">
                                {t('highlights.marketDesc')}
                            </p>
                        </div>
                        <div className="mt-6 flex flex-wrap gap-2">
                            {marketTags.map((tag) => (
                                <span
                                    key={tag}
                                    className="rounded-md border border-slate-900/10 bg-white px-2.5 py-1 text-xs font-medium text-slate-700 dark:border-white/10 dark:bg-white/5 dark:text-slate-200"
                                >
                                    {tag}
                                </span>
                            ))}
                        </div>
                    </article>
                </div>

                <div className="mt-8 grid gap-x-8 gap-y-8 border-t border-slate-900/10 pt-8 sm:grid-cols-2 lg:grid-cols-4 dark:border-white/10">
                    {pillars.map((pillar) => (
                        <div key={pillar.title}>
                            <pillar.icon className="size-5 text-[#1d4ed8] dark:text-[#60a5fa]" aria-hidden="true" />
                            <h3 className="mt-3.5 text-base font-semibold text-slate-950 dark:text-white">
                                {pillar.title}
                            </h3>
                            <p className="mt-2 text-sm leading-relaxed text-slate-600 dark:text-slate-300">
                                {pillar.desc}
                            </p>
                        </div>
                    ))}
                </div>
            </div>
        </section>
    );
}

// MediaSection 接入协议与清算台账: 左侧说明统一入站协议与美元基准，右侧按模态列出计费规则。
function MediaSection() {
    const t = useTranslations('landing');
    const modalities: { icon: LucideIcon; title: string; desc: string; price: string }[] = [
        { icon: MessageSquareText, title: t('media.textTitle'), desc: t('media.textDesc'), price: t('media.textPrice') },
        { icon: ImageIcon, title: t('media.imageTitle'), desc: t('media.imageDesc'), price: t('media.imagePrice') },
        { icon: Video, title: t('media.videoTitle'), desc: t('media.videoDesc'), price: t('media.videoPrice') },
    ];
    return (
        <section id="media" className="scroll-mt-16 border-t border-slate-900/10 dark:border-white/10">
            <div className="mx-auto grid max-w-6xl gap-10 px-4 py-16 md:px-6 md:py-22 lg:grid-cols-12 lg:items-start">
                <div className="lg:col-span-5">
                    <h2 className="text-2xl font-semibold tracking-tight text-slate-950 md:text-3xl dark:text-white">
                        {t('media.title')}
                    </h2>
                    <p className="mt-3 text-base leading-relaxed text-slate-600 dark:text-slate-300">
                        {t('media.subtitle')}
                    </p>
                    <div className="mt-6 rounded-xl border border-slate-900/10 bg-white p-5 dark:border-white/10 dark:bg-[#111b2e]">
                        <div className="flex items-center gap-2 text-sm font-semibold text-slate-950 dark:text-white">
                            <Radio className="size-4 text-[#1d4ed8] dark:text-[#60a5fa]" />
                            <span>{t('media.protocolsTitle')}</span>
                        </div>
                        <p className="mt-2.5 text-sm leading-relaxed text-slate-600 dark:text-slate-300">
                            {t('media.protocolsDesc')}
                        </p>
                    </div>
                </div>
                <div className="divide-y divide-slate-900/10 rounded-2xl border border-slate-900/10 bg-white dark:divide-white/10 dark:border-white/10 dark:bg-[#111b2e] lg:col-span-7">
                    {modalities.map((item) => (
                        <div key={item.title} className="flex flex-col gap-3 p-5 sm:flex-row sm:items-start sm:justify-between sm:gap-6 sm:p-6">
                            <div className="flex items-start gap-3.5">
                                <div className="mt-0.5 flex size-9 shrink-0 items-center justify-center rounded-lg bg-[#1d4ed8]/10 text-[#1d4ed8] dark:bg-[#3b82f6]/15 dark:text-[#60a5fa]">
                                    <item.icon className="size-4" />
                                </div>
                                <div>
                                    <h3 className="font-semibold text-slate-950 dark:text-white">{item.title}</h3>
                                    <p className="mt-1 text-sm leading-relaxed text-slate-600 dark:text-slate-300">
                                        {item.desc}
                                    </p>
                                </div>
                            </div>
                            <div className="shrink-0 rounded-lg bg-slate-100 px-3 py-1.5 text-xs font-medium text-slate-800 sm:text-right dark:bg-white/8 dark:text-slate-200">
                                {item.price}
                            </div>
                        </div>
                    ))}
                </div>
            </div>
        </section>
    );
}

// RolesSection 适用角色: 管理员 / 渠道商 / 用户三种视角的权限边界。
function RolesSection() {
    const t = useTranslations('landing');
    const readPoints = (role: 'admin' | 'reseller' | 'user') =>
        ['p1', 'p2', 'p3'].map((key) => t(`rolePoints.${role}.${key}`));
    const roles: { icon: LucideIcon; title: string; desc: string; points: string[] }[] = [
        { icon: ShieldCheck, title: t('roles.adminTitle'), desc: t('roles.adminDesc'), points: readPoints('admin') },
        { icon: Store, title: t('roles.resellerTitle'), desc: t('roles.resellerDesc'), points: readPoints('reseller') },
        { icon: UserRound, title: t('roles.userTitle'), desc: t('roles.userDesc'), points: readPoints('user') },
    ];
    return (
        <section id="roles" className="scroll-mt-16 border-t border-slate-900/10 bg-white dark:border-white/10 dark:bg-[#0f172a]">
            <div className="mx-auto max-w-6xl px-4 py-16 md:px-6 md:py-22">
                <div className="max-w-2xl">
                    <h2 className="text-2xl font-semibold tracking-tight text-slate-950 md:text-3xl dark:text-white">
                        {t('roles.title')}
                    </h2>
                    <p className="mt-3 text-base leading-relaxed text-slate-600 dark:text-slate-300">
                        {t('roles.subtitle')}
                    </p>
                </div>
                <div className="mt-10 grid gap-6 md:grid-cols-3">
                    {roles.map((role) => (
                        <div
                            key={role.title}
                            className="flex flex-col rounded-2xl border border-slate-900/10 bg-[#f6f8fb] p-6 dark:border-white/10 dark:bg-[#111b2e]"
                        >
                            <div className="flex items-center gap-3">
                                <div className="flex size-9 items-center justify-center rounded-lg bg-[#1d4ed8]/10 text-[#1d4ed8] dark:bg-[#3b82f6]/15 dark:text-[#60a5fa]">
                                    <role.icon className="size-4" />
                                </div>
                                <h3 className="text-lg font-semibold text-slate-950 dark:text-white">{role.title}</h3>
                            </div>
                            <p className="mt-3 text-sm leading-relaxed text-slate-600 dark:text-slate-300">{role.desc}</p>
                            <ul className="mt-5 space-y-2.5 border-t border-slate-900/8 pt-4 dark:border-white/10">
                                {role.points.map((point) => (
                                    <li key={point} className="flex items-start gap-2 text-sm text-slate-700 dark:text-slate-200">
                                        <Check className="mt-0.5 size-4 shrink-0 text-[#1d4ed8] dark:text-[#60a5fa]" aria-hidden="true" />
                                        <span>{point}</span>
                                    </li>
                                ))}
                            </ul>
                        </div>
                    ))}
                </div>
            </div>
        </section>
    );
}

// CtaSection 底部行动区: 深色建筑感收束带，提供控制台入口与公开状态页直达。
function CtaSection({ onEnter }: { onEnter: () => void }) {
    const t = useTranslations('landing');
    return (
        <section className="border-t border-slate-900/10 dark:border-white/10">
            <div className="mx-auto max-w-6xl px-4 py-16 md:px-6 md:py-20">
                <div className="flex flex-col justify-between gap-8 rounded-2xl bg-[#0f172a] px-6 py-10 text-white sm:px-10 sm:py-12 lg:flex-row lg:items-center dark:border dark:border-white/12 dark:bg-[#111b2e]">
                    <div className="max-w-xl">
                        <h2 className="text-2xl font-semibold tracking-tight md:text-3xl">{t('cta.title')}</h2>
                        <p className="mt-2.5 text-sm leading-relaxed text-slate-300 md:text-base">{t('cta.subtitle')}</p>
                    </div>
                    <div className="flex flex-wrap items-center gap-3">
                        <Button
                            size="lg"
                            className="h-11 rounded-lg bg-white px-6 text-sm font-semibold text-slate-950 hover:bg-slate-100"
                            onClick={onEnter}
                        >
                            {t('cta.button')}
                            <ArrowRight className="size-4" />
                        </Button>
                        <a
                            href="/status"
                            className="inline-flex h-11 items-center justify-center gap-1.5 rounded-lg border border-white/20 px-5 text-sm font-medium text-white transition-colors hover:bg-white/10"
                        >
                            {t('cta.statusButton')}
                            <ArrowUpRight className="size-4" />
                        </a>
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
        <footer className="border-t border-slate-900/10 dark:border-white/10">
            <div className="mx-auto flex max-w-6xl flex-col items-start justify-between gap-4 px-4 py-10 sm:flex-row sm:items-center md:px-6">
                <div className="space-y-1">
                    <div className="flex items-center gap-2.5 text-slate-950 dark:text-white">
                        <Logo size={24} />
                        <span className="text-sm font-semibold">{siteName}</span>
                    </div>
                    <p className="text-xs text-slate-500 dark:text-slate-400">{t('footer.tagline')}</p>
                </div>
                <div className="flex flex-wrap items-center gap-4 text-xs text-slate-500 dark:text-slate-400">
                    <span className="inline-flex items-center gap-1.5">
                        <Languages className="size-3.5" aria-hidden="true" />
                        {t('hero.chip3')}
                    </span>
                    {contact &&
                        (href ? (
                            <a className="underline underline-offset-2 hover:text-slate-900 dark:hover:text-white" href={href}>
                                {t('footer.contact')}
                            </a>
                        ) : (
                            <span>{contact}</span>
                        ))}
                    <span>
                        {new Date().getFullYear()} {siteName}
                    </span>
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
        <div className="min-h-dvh scroll-smooth bg-[#f6f8fb] text-slate-900 antialiased dark:bg-[#0b1220] dark:text-slate-100">
            <LandingHeader siteName={siteName} onEnter={onEnter} />
            <main>
                <HeroSection onEnter={onEnter} />
                <MetricsBand />
                <FlowSection />
                <FeaturesSection />
                <MediaSection />
                <RolesSection />
                <CtaSection onEnter={onEnter} />
            </main>
            <LandingFooter siteName={siteName} site={site} />
        </div>
    );
}
