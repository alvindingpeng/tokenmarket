import { useState } from 'react';
import { AnimatePresence, MotionConfig, motion } from 'motion/react';
import { useTranslations } from 'use-intl';
import type { LucideIcon } from 'lucide-react';
import {
    Activity,
    ArrowRight,
    ArrowUpRight,
    Cable,
    Check,
    Cpu,
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
    Zap,
} from 'lucide-react';
import heroCoreImg from '@/assets/landing/hero-core.jpg';
import smartRoutingImg from '@/assets/landing/smart-routing.jpg';
import channelMarketImg from '@/assets/landing/channel-market.jpg';
import Logo from '@/components/modules/logo';
import { SiteBanners } from '@/components/site-banners';
import { Button } from '@/components/ui/button';
import { LanguageSwitch } from '@/components/language-switch';
import { DEFAULT_SITE_NAME, useSiteConfig, type SiteConfig } from '@/api/site';

type WorkbenchScenarioID = 'coding' | 'summary' | 'media';

const SCENARIO_IDS: WorkbenchScenarioID[] = ['coding', 'summary', 'media'];

const REVEAL_VIEWPORT = { once: true, amount: 0.18 } as const;

// LandingHeader 顶部吸顶栏: 与内页冷白工作台一致的半透明磨砂顶栏，保留品牌、导航、语言与入口。
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
        <header className="sticky top-0 z-50 border-b border-slate-200/80 bg-[#f7f8fa]/85 backdrop-blur-xl dark:border-white/10 dark:bg-[#0b1220]/85">
            <div className="mx-auto flex min-h-16 max-w-6xl flex-wrap items-center justify-between gap-3 px-4 py-2.5 md:flex-nowrap md:px-6 md:py-0">
                <a href="#top" className="group flex items-center gap-2.5 text-slate-900 dark:text-white">
                    <span className="relative flex items-center justify-center rounded-lg bg-white p-0.5 shadow-xs ring-1 ring-slate-200/90 transition-all group-hover:ring-[#315fbd]/40 dark:bg-white/5 dark:ring-white/15">
                        <Logo size={30} />
                    </span>
                    <span className="text-base font-semibold tracking-tight md:text-lg">{siteName}</span>
                </a>
                <nav className="hidden items-center gap-1 md:flex" aria-label="Main">
                    {links.map((link) => (
                        <a
                            key={link.href}
                            href={link.href}
                            className="rounded-lg px-3 py-1.5 text-sm font-medium text-slate-600 transition-colors hover:bg-[#315fbd]/[0.07] hover:text-[#315fbd] dark:text-slate-300 dark:hover:bg-white/[0.06] dark:hover:text-cyan-200"
                        >
                            {link.label}
                        </a>
                    ))}
                </nav>
                <div className="flex w-full items-center justify-between gap-2 sm:w-auto sm:justify-end">
                    <a
                        href="/status"
                        className="inline-flex min-h-10 items-center gap-1.5 text-sm font-medium text-[#315fbd] hover:text-[#234899] dark:text-cyan-300 md:hidden"
                    >
                        <span className="size-2 rounded-full bg-emerald-500 shadow-[0_0_8px_rgba(16,185,129,0.7)]" />
                        {t('nav.status')}
                    </a>
                    <div className="flex items-center gap-2">
                        <LanguageSwitch />
                        <Button
                            size="sm"
                            className="rounded-lg bg-gradient-to-r from-[#315fbd] to-[#0ea5e9] px-4 font-semibold text-white shadow-[0_8px_20px_-6px_rgba(49,95,189,0.55)] transition-all hover:brightness-105"
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

// RoutingTopologyPulse 调度台顶部动态光路拓扑: 亮白光学面板上呈现入站、核心路由与主备热切光流。
function RoutingTopologyPulse({ scenario }: { scenario: WorkbenchScenarioID }) {
    const accent =
        scenario === 'coding' ? '#315fbd' : scenario === 'summary' ? '#0ea5e9' : '#6366f1';

    return (
        <div className="relative overflow-hidden border-b border-slate-200/80 bg-gradient-to-r from-[#f0f5ff] via-[#f7faff] to-[#eef9ff] px-4 py-3 dark:border-white/10 dark:bg-[#071124] sm:px-5">
            <svg
                viewBox="0 0 420 56"
                className="h-12 w-full"
                fill="none"
                aria-hidden="true"
            >
                <defs>
                    <linearGradient id="route-active-grad-light" x1="0%" y1="0%" x2="100%" y2="0%">
                        <stop offset="0%" stopColor={accent} stopOpacity="0.35" />
                        <stop offset="50%" stopColor={accent} stopOpacity="1" />
                        <stop offset="100%" stopColor="#10b981" stopOpacity="1" />
                    </linearGradient>
                </defs>

                {/* Base tracks */}
                <path
                    d="M 28 28 L 170 28 C 215 28, 225 14, 270 14 L 392 14"
                    stroke="rgba(49,95,189,0.18)"
                    strokeWidth="2.2"
                />
                <path
                    d="M 170 28 C 215 28, 225 42, 270 42 L 392 42"
                    stroke="rgba(100,116,139,0.22)"
                    strokeWidth="1.5"
                    strokeDasharray="4 4"
                />

                {/* Animated luminous packet stream on active route */}
                <path
                    d="M 28 28 L 170 28 C 215 28, 225 14, 270 14 L 392 14"
                    stroke="url(#route-active-grad-light)"
                    strokeWidth="2.8"
                    strokeLinecap="round"
                    className="landing-packet-animate"
                />

                {/* Inbound node */}
                <circle cx="28" cy="28" r="5.5" fill="#ffffff" stroke={accent} strokeWidth="2.2" />
                <circle cx="28" cy="28" r="2.2" fill={accent} />

                {/* Router Core node */}
                <rect
                    x="156"
                    y="15"
                    width="26"
                    height="26"
                    rx="7"
                    fill="#ffffff"
                    stroke={accent}
                    strokeWidth="1.8"
                />
                <circle cx="169" cy="28" r="4.5" fill={accent} opacity="0.92" />

                {/* Primary active target node */}
                <circle cx="392" cy="14" r="5.5" fill="#ecfdf5" stroke="#10b981" strokeWidth="2.2" />
                <circle cx="392" cy="14" r="2.4" fill="#10b981" />

                {/* Standby target node */}
                <circle cx="392" cy="42" r="4.5" fill="#ffffff" stroke="rgba(100,116,139,0.45)" strokeWidth="1.6" />
            </svg>
        </div>
    );
}

// SmartRoutingWorkbench 首屏交互式编组调度台: 亮白精密玻璃面板，实时切换场景查看光路、评分选路、热备切道与账本结算。
function SmartRoutingWorkbench() {
    const t = useTranslations('landing');
    const [active, setActive] = useState<WorkbenchScenarioID>('coding');
    const prefix = `workbench.scenarios.${active}`;

    return (
        <div className="relative overflow-hidden rounded-2xl border border-slate-200/90 bg-white/95 shadow-[0_28px_70px_-24px_rgba(49,95,189,0.22)] backdrop-blur-xl dark:border-white/12 dark:bg-[#0f172a]/90">
            {/* Subtle top cobalt-cyan rim */}
            <div
                aria-hidden="true"
                className="pointer-events-none absolute inset-x-0 top-0 h-[2px] bg-gradient-to-r from-[#315fbd] via-[#0ea5e9] to-emerald-400"
            />

            <div className="flex flex-wrap items-center justify-between gap-3 border-b border-slate-200/80 px-4 py-3.5 dark:border-white/10 sm:px-5">
                <div className="flex items-center gap-2 text-xs font-semibold text-slate-800 dark:text-slate-100">
                    <span className="relative flex size-2.5">
                        <span className="absolute inline-flex size-full animate-ping rounded-full bg-emerald-500 opacity-70" />
                        <span className="relative inline-flex size-2.5 rounded-full bg-emerald-500" />
                    </span>
                    <Sparkles className="size-3.5 text-[#315fbd] dark:text-cyan-300" aria-hidden="true" />
                    <span>{t('workbench.heading')}</span>
                    <span className="hidden rounded-md border border-emerald-500/25 bg-emerald-50 px-1.5 py-0.5 text-[10px] font-medium text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300 sm:inline-block">
                        {t('workbench.statusLive')}
                    </span>
                </div>
                <div
                    role="tablist"
                    aria-label={t('workbench.heading')}
                    className="inline-flex rounded-lg border border-slate-200/90 bg-[#f4f6fb] p-0.5 dark:border-white/10 dark:bg-white/[0.05]"
                >
                    {SCENARIO_IDS.map((id) => {
                        const selected = id === active;
                        return (
                            <button
                                key={id}
                                type="button"
                                role="tab"
                                aria-selected={selected}
                                onClick={() => setActive(id)}
                                className={`relative rounded-md px-2.5 py-1 text-xs font-medium transition-colors ${
                                    selected
                                        ? 'text-white'
                                        : 'text-slate-600 hover:text-slate-900 dark:text-slate-300 dark:hover:text-white'
                                }`}
                            >
                                {selected && (
                                    <motion.span
                                        layoutId="landing-workbench-pill"
                                        transition={{ type: 'spring', stiffness: 420, damping: 32 }}
                                        className="absolute inset-0 rounded-md bg-gradient-to-r from-[#315fbd] to-[#0ea5e9] shadow-[0_4px_12px_-2px_rgba(49,95,189,0.45)]"
                                    />
                                )}
                                <span className="relative z-10">{t(`workbench.tabs.${id}`)}</span>
                            </button>
                        );
                    })}
                </div>
            </div>

            <RoutingTopologyPulse scenario={active} />

            <AnimatePresence mode="wait">
                <motion.div
                    key={active}
                    initial={{ opacity: 0, y: 8 }}
                    animate={{ opacity: 1, y: 0 }}
                    exit={{ opacity: 0, y: -6 }}
                    transition={{ duration: 0.22, ease: 'easeOut' }}
                    className="divide-y divide-slate-200/75 dark:divide-white/10"
                >
                    <div className="px-4 py-3.5 sm:px-5">
                        <div className="flex flex-wrap items-center justify-between gap-2">
                            <span className="text-xs font-medium text-slate-500 dark:text-slate-400">
                                {t('workbench.inboundLabel')}
                            </span>
                            <code className="rounded-md border border-[#315fbd]/15 bg-[#eef4ff] px-2 py-0.5 font-mono text-xs font-medium text-[#315fbd] dark:border-cyan-400/20 dark:bg-cyan-400/10 dark:text-cyan-200">
                                {t(`${prefix}.endpoint`)}
                            </code>
                        </div>
                        <div className="mt-2 flex flex-wrap items-baseline justify-between gap-2">
                            <span className="text-sm font-semibold text-slate-900 dark:text-white">
                                {t(`${prefix}.protocol`)}
                            </span>
                            <span className="font-mono text-xs font-medium text-[#0284c7] dark:text-cyan-300">
                                {t(`${prefix}.model`)}
                            </span>
                        </div>
                    </div>

                    <div className="px-4 py-3.5 sm:px-5">
                        <div className="text-xs font-medium text-slate-500 dark:text-slate-400">
                            {t('workbench.routingLabel')}
                        </div>
                        <p className="mt-1.5 text-sm leading-relaxed text-slate-700 dark:text-slate-200">
                            {t(`${prefix}.policy`)}
                        </p>
                    </div>

                    <div className="space-y-2.5 px-4 py-3.5 sm:px-5">
                        <div className="text-xs font-medium text-slate-500 dark:text-slate-400">
                            {t('workbench.tracksLabel')}
                        </div>
                        <div className="flex items-start gap-2.5 rounded-xl border border-emerald-500/25 bg-emerald-50/75 px-3 py-2.5 dark:border-emerald-400/30 dark:bg-emerald-400/[0.08]">
                            <span className="mt-0.5 shrink-0 rounded bg-emerald-600 px-1.5 py-0.5 text-[11px] font-semibold text-white dark:bg-emerald-400 dark:text-slate-950">
                                {t('workbench.primaryBadge')}
                            </span>
                            <span className="text-xs leading-relaxed text-slate-800 dark:text-slate-100">
                                {t(`${prefix}.trackA`)}
                            </span>
                        </div>
                        <div className="flex items-start gap-2.5 rounded-xl border border-slate-200/80 bg-[#f8fafd] px-3 py-2.5 dark:border-white/10 dark:bg-white/[0.03]">
                            <span className="mt-0.5 shrink-0 rounded bg-slate-200/90 px-1.5 py-0.5 text-[11px] font-medium text-slate-700 dark:bg-white/12 dark:text-slate-300">
                                {t('workbench.standbyBadge')}
                            </span>
                            <span className="text-xs leading-relaxed text-slate-600 dark:text-slate-300">
                                {t(`${prefix}.trackB`)}
                            </span>
                        </div>
                    </div>

                    <div className="bg-[#f5f8ff]/80 px-4 py-3.5 dark:bg-[#0a1326] sm:px-5">
                        <div className="text-xs font-medium text-slate-500 dark:text-slate-400">
                            {t('workbench.ledgerLabel')}
                        </div>
                        <p className="mt-1 text-xs leading-relaxed text-slate-700 dark:text-slate-200">
                            {t(`${prefix}.ledger`)}
                        </p>
                    </div>
                </motion.div>
            </AnimatePresence>
        </div>
    );
}

// HeroSection 首屏: 与内页一致的冷白底色 + 精密科技网格与柔和蓝青呼吸光晕，左侧渐变标题与入口，右侧交互式调度台。
function HeroSection({ onEnter }: { onEnter: () => void }) {
    const t = useTranslations('landing');
    const { data: site } = useSiteConfig();
    const subtitle =
        site?.site_description?.trim() && site.site_description.trim() !== t('hero.subtitle')
            ? site.site_description.trim()
            : t('hero.subtitle');

    const chips = [t('hero.chip1'), t('hero.chip2'), t('hero.chip4')];

    return (
        <section id="top" className="relative overflow-x-clip">
            {/* Precision blueprint grid & subtle airy blue-cyan halos */}
            <div aria-hidden="true" className="landing-tech-grid pointer-events-none absolute inset-0" />
            <div
                aria-hidden="true"
                className="landing-orb-animate pointer-events-none absolute -top-24 left-1/4 h-[28rem] w-[28rem] -translate-x-1/2 rounded-full bg-[#315fbd]/12 blur-[110px] dark:bg-cyan-500/18"
            />
            <div
                aria-hidden="true"
                className="landing-orb-animate pointer-events-none absolute top-12 right-0 h-[26rem] w-[26rem] rounded-full bg-[#0ea5e9]/12 blur-[110px] dark:bg-blue-600/18"
                style={{ animationDelay: '2.5s' }}
            />

            <div className="relative mx-auto grid max-w-6xl gap-10 px-4 pt-12 pb-12 md:px-6 md:pt-20 md:pb-16 lg:grid-cols-12 lg:items-center lg:gap-12">
                <motion.div
                    initial={{ opacity: 0, y: 18 }}
                    animate={{ opacity: 1, y: 0 }}
                    transition={{ duration: 0.55, ease: 'easeOut' }}
                    className="lg:col-span-6"
                >
                    <div className="inline-flex items-center gap-2 rounded-full border border-[#315fbd]/20 bg-white/90 px-3.5 py-1 text-xs font-semibold text-[#315fbd] shadow-xs backdrop-blur-md dark:border-cyan-400/30 dark:bg-cyan-400/10 dark:text-cyan-200">
                        <Zap className="size-3.5 text-[#0ea5e9]" aria-hidden="true" />
                        <span>{t('hero.badge')}</span>
                    </div>

                    <h1 className="mt-5 max-w-xl bg-gradient-to-br from-slate-950 via-slate-900 to-[#315fbd] bg-clip-text text-3xl font-semibold tracking-[-0.03em] text-transparent sm:text-4xl lg:text-[3.2rem] lg:leading-[1.12] dark:from-white dark:via-slate-100 dark:to-cyan-200">
                        {t('hero.title')}
                    </h1>
                    <p className="mt-5 max-w-xl text-base leading-relaxed text-slate-600 md:text-lg dark:text-slate-300">
                        {subtitle}
                    </p>

                    <div className="mt-8 flex flex-wrap items-center gap-3">
                        <Button
                            size="lg"
                            className="h-11 rounded-lg bg-gradient-to-r from-[#315fbd] via-[#2563eb] to-[#0ea5e9] px-6 text-sm font-semibold text-white shadow-[0_14px_30px_-10px_rgba(49,95,189,0.65)] transition-all hover:scale-[1.01] hover:brightness-105"
                            onClick={onEnter}
                        >
                            {t('hero.ctaPrimary')}
                            <ArrowRight className="size-4" />
                        </Button>
                        <a
                            href="/status"
                            className="inline-flex h-11 items-center justify-center gap-2 rounded-lg border border-slate-200/95 bg-white px-5 text-sm font-medium text-slate-800 shadow-xs transition-all hover:border-[#315fbd]/35 hover:bg-[#f4f7ff] dark:border-white/15 dark:bg-white/[0.05] dark:text-slate-100 dark:hover:bg-white/10"
                        >
                            <Activity className="size-4 text-emerald-600 dark:text-emerald-400" />
                            {t('cta.statusButton')}
                        </a>
                    </div>

                    <div className="mt-7 flex flex-wrap gap-2">
                        {chips.map((chip) => (
                            <span
                                key={chip}
                                className="rounded-md border border-slate-200/90 bg-white/85 px-2.5 py-1 text-xs font-medium text-slate-600 shadow-2xs dark:border-white/10 dark:bg-white/[0.04] dark:text-slate-300"
                            >
                                {chip}
                            </span>
                        ))}
                    </div>

                    {/* 维护模式对访客同样可见: 被维护挡住的人需要知道登不上的原因。 */}
                    <SiteBanners className="mt-6" includeAnnouncement={false} />
                </motion.div>

                <motion.div
                    initial={{ opacity: 0, y: 22 }}
                    animate={{ opacity: 1, y: 0 }}
                    transition={{ duration: 0.65, delay: 0.12, ease: 'easeOut' }}
                    className="lg:col-span-6"
                >
                    <SmartRoutingWorkbench />
                </motion.div>
            </div>
        </section>
    );
}

// CoreShowcaseMetrics 光子编组核心视觉展板 + 架构规格带: 纯白精密工业风 3D 光学核心配图，底部衔接 4 项核心指标。
function CoreShowcaseMetrics() {
    const t = useTranslations('landing');
    const items = [
        { value: t('metrics.m1Value'), label: t('metrics.m1Label') },
        { value: t('metrics.m2Value'), label: t('metrics.m2Label') },
        { value: t('metrics.m3Value'), label: t('metrics.m3Label') },
        { value: t('metrics.m4Value'), label: t('metrics.m4Label') },
    ];
    return (
        <section className="relative overflow-x-clip px-4 pb-12 md:px-6 md:pb-16">
            <motion.div
                initial={{ opacity: 0, y: 24 }}
                whileInView={{ opacity: 1, y: 0 }}
                viewport={REVEAL_VIEWPORT}
                transition={{ duration: 0.6, ease: 'easeOut' }}
                className="relative mx-auto max-w-6xl overflow-hidden rounded-3xl border border-slate-200/90 bg-white shadow-[0_28px_80px_-30px_rgba(49,95,189,0.2)] dark:border-white/12 dark:bg-[#0f172a]"
            >
                {/* Top sweeping cobalt-cyan laser beam */}
                <div aria-hidden="true" className="pointer-events-none absolute inset-x-0 top-0 z-10 h-[2px] overflow-hidden">
                    <div className="landing-beam-animate h-full w-1/3 bg-gradient-to-r from-transparent via-[#0ea5e9] to-transparent" />
                </div>

                <div className="relative aspect-[16/9] max-h-[420px] w-full overflow-hidden bg-[#f4f7fb] sm:aspect-[21/9]">
                    <img
                        src={heroCoreImg}
                        alt={t('hero.badge')}
                        className="h-full w-full object-cover object-center transition-transform duration-1000 hover:scale-[1.03]"
                    />
                    {/* Subtle optical scanline */}
                    <div
                        aria-hidden="true"
                        className="landing-scanline-animate pointer-events-none absolute inset-x-0 top-0 h-20 bg-gradient-to-b from-transparent via-[#0ea5e9]/10 to-transparent"
                    />
                    <div
                        aria-hidden="true"
                        className="pointer-events-none absolute inset-x-0 bottom-0 h-20 bg-gradient-to-t from-white/95 via-white/30 to-transparent dark:from-[#0f172a]/95 dark:via-[#0f172a]/30"
                    />
                </div>

                <div className="relative grid grid-cols-2 divide-y divide-slate-200/80 border-t border-slate-200/80 bg-white/95 backdrop-blur-md dark:divide-white/10 dark:border-white/10 dark:bg-[#0f172a]/90 sm:divide-y-0 lg:grid-cols-4 lg:divide-x">
                    {items.map((item, idx) => (
                        <motion.div
                            key={item.label}
                            initial={{ opacity: 0, y: 12 }}
                            whileInView={{ opacity: 1, y: 0 }}
                            viewport={REVEAL_VIEWPORT}
                            transition={{ duration: 0.4, delay: idx * 0.06 }}
                            className="px-4 py-5 sm:px-6 lg:py-6"
                        >
                            <div className="bg-gradient-to-r from-slate-950 to-[#315fbd] bg-clip-text text-xl font-semibold tracking-tight text-transparent tabular-nums dark:from-white dark:to-cyan-200 md:text-2xl">
                                {item.value}
                            </div>
                            <div className="mt-1 text-xs leading-relaxed text-slate-600 dark:text-slate-400 sm:text-sm">
                                {item.label}
                            </div>
                        </motion.div>
                    ))}
                </div>
            </motion.div>
        </section>
    );
}

// FlowSection 编组流程: 汇入 → 编组 → 发车 → 计量，带动态扫描光束管线。
function FlowSection() {
    const t = useTranslations('landing');
    const steps: { icon: LucideIcon; title: string; desc: string }[] = [
        { icon: Cable, title: t('flow.step1Title'), desc: t('flow.step1Desc') },
        { icon: GitMerge, title: t('flow.step2Title'), desc: t('flow.step2Desc') },
        { icon: Send, title: t('flow.step3Title'), desc: t('flow.step3Desc') },
        { icon: Receipt, title: t('flow.step4Title'), desc: t('flow.step4Desc') },
    ];
    return (
        <section id="flow" className="relative scroll-mt-16 border-t border-slate-200/80 dark:border-white/10">
            <div className="mx-auto max-w-6xl px-4 py-16 md:px-6 md:py-22">
                <motion.div
                    initial={{ opacity: 0, y: 16 }}
                    whileInView={{ opacity: 1, y: 0 }}
                    viewport={REVEAL_VIEWPORT}
                    transition={{ duration: 0.45 }}
                    className="max-w-2xl"
                >
                    <h2 className="text-2xl font-semibold tracking-tight text-slate-950 dark:text-white md:text-3xl">
                        {t('flow.title')}
                    </h2>
                    <p className="mt-3 text-base leading-relaxed text-slate-600 dark:text-slate-300">
                        {t('flow.subtitle')}
                    </p>
                </motion.div>

                <div className="relative mt-12">
                    {/* Desktop animated conduit line */}
                    <div
                        aria-hidden="true"
                        className="pointer-events-none absolute top-0 right-0 left-0 hidden h-[2px] overflow-hidden bg-slate-200/90 dark:bg-white/10 lg:block"
                    >
                        <div className="landing-beam-animate h-full w-1/3 bg-gradient-to-r from-transparent via-[#315fbd] to-transparent dark:via-cyan-400" />
                    </div>

                    <ol className="grid gap-5 sm:grid-cols-2 lg:grid-cols-4">
                        {steps.map((step, index) => (
                            <motion.li
                                key={step.title}
                                initial={{ opacity: 0, y: 18 }}
                                whileInView={{ opacity: 1, y: 0 }}
                                viewport={REVEAL_VIEWPORT}
                                transition={{ duration: 0.45, delay: index * 0.08 }}
                                className="group relative flex flex-col rounded-2xl border border-slate-200/85 bg-white p-5 shadow-[0_8px_24px_-16px_rgba(15,23,42,0.12)] transition-all hover:-translate-y-0.5 hover:border-[#315fbd]/40 hover:shadow-[0_18px_36px_-18px_rgba(49,95,189,0.25)] dark:border-white/10 dark:bg-[#0f172a]"
                            >
                                <div className="flex items-center justify-between">
                                    <span className="rounded-md border border-[#315fbd]/20 bg-[#eef4ff] px-2 py-0.5 font-mono text-xs font-semibold text-[#315fbd] tabular-nums dark:border-cyan-400/25 dark:bg-cyan-400/10 dark:text-cyan-300">
                                        {String(index + 1).padStart(2, '0')}
                                    </span>
                                    <step.icon
                                        className="size-4 text-slate-400 transition-colors group-hover:text-[#315fbd] dark:group-hover:text-cyan-300"
                                        aria-hidden="true"
                                    />
                                </div>
                                <h3 className="mt-4 text-base font-semibold text-slate-950 dark:text-white">{step.title}</h3>
                                <p className="mt-2 text-sm leading-relaxed text-slate-600 dark:text-slate-300">{step.desc}</p>
                            </motion.li>
                        ))}
                    </ol>
                </div>
            </div>
        </section>
    );
}

// FeaturesSection 核心能力: 顶部两张亮白工业风 3D 配图大卡突出智能路由与模型广场，下方四列核心支柱卡片。
function FeaturesSection() {
    const t = useTranslations('landing');
    const routingTags = ['t1', 't2', 't3', 't4', 't5'].map((k) => t(`highlights.smartRoutingTags.${k}`));
    const marketTags = ['t1', 't2', 't3', 't4', 't5'].map((k) => t(`highlights.marketTags.${k}`));
    const pillars: { icon: LucideIcon; title: string; desc: string }[] = [
        { icon: Cpu, title: t('features.channelsTitle'), desc: t('highlights.channelsDetail') },
        { icon: Wallet, title: t('features.billingTitle'), desc: t('highlights.billingDetail') },
        { icon: HeartPulse, title: t('features.opsTitle'), desc: t('highlights.opsDetail') },
        { icon: KeyRound, title: t('features.accessTitle'), desc: t('highlights.accessDetail') },
    ];

    return (
        <section id="features" className="relative scroll-mt-16 border-t border-slate-200/80 bg-white dark:border-white/10 dark:bg-[#0c1426]">
            <div className="mx-auto max-w-6xl px-4 py-16 md:px-6 md:py-22">
                <motion.div
                    initial={{ opacity: 0, y: 16 }}
                    whileInView={{ opacity: 1, y: 0 }}
                    viewport={REVEAL_VIEWPORT}
                    transition={{ duration: 0.45 }}
                    className="max-w-2xl"
                >
                    <h2 className="text-2xl font-semibold tracking-tight text-slate-950 dark:text-white md:text-3xl">
                        {t('features.title')}
                    </h2>
                    <p className="mt-3 text-base leading-relaxed text-slate-600 dark:text-slate-300">
                        {t('features.subtitle')}
                    </p>
                </motion.div>

                <div className="mt-10 grid gap-6 lg:grid-cols-2">
                    {/* Card 1: Smart Routing with Light 3D Visual */}
                    <motion.article
                        initial={{ opacity: 0, y: 20 }}
                        whileInView={{ opacity: 1, y: 0 }}
                        viewport={REVEAL_VIEWPORT}
                        transition={{ duration: 0.5 }}
                        className="group flex flex-col overflow-hidden rounded-2xl border border-slate-200/90 bg-[#f9fbff] shadow-[0_14px_40px_-24px_rgba(49,95,189,0.2)] transition-all hover:border-[#315fbd]/40 hover:shadow-[0_24px_56px_-24px_rgba(49,95,189,0.28)] dark:border-white/12 dark:bg-[#111b2e]"
                    >
                        <div className="relative aspect-[16/9] overflow-hidden border-b border-slate-200/70 bg-[#f4f7fb] dark:border-white/10">
                            <img
                                src={smartRoutingImg}
                                alt={t('highlights.smartRoutingTitle')}
                                loading="lazy"
                                className="h-full w-full object-cover transition-transform duration-700 group-hover:scale-105"
                            />
                            <div className="absolute top-4 left-4 inline-flex size-10 items-center justify-center rounded-xl border border-slate-200/90 bg-white/90 text-[#315fbd] shadow-sm backdrop-blur-md dark:border-white/15 dark:bg-[#0f172a]/80 dark:text-cyan-300">
                                <FolderTree className="size-5" />
                            </div>
                        </div>
                        <div className="flex flex-1 flex-col justify-between p-6 sm:p-7">
                            <div>
                                <h3 className="text-xl font-semibold tracking-tight text-slate-950 dark:text-white">
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
                                        className="rounded-md border border-[#315fbd]/15 bg-white px-2.5 py-1 text-xs font-medium text-[#315fbd] shadow-2xs dark:border-cyan-400/20 dark:bg-cyan-400/10 dark:text-cyan-200"
                                    >
                                        {tag}
                                    </span>
                                ))}
                            </div>
                        </div>
                    </motion.article>

                    {/* Card 2: Channel Marketplace & Settlement with Light 3D Visual */}
                    <motion.article
                        initial={{ opacity: 0, y: 20 }}
                        whileInView={{ opacity: 1, y: 0 }}
                        viewport={REVEAL_VIEWPORT}
                        transition={{ duration: 0.5, delay: 0.1 }}
                        className="group flex flex-col overflow-hidden rounded-2xl border border-slate-200/90 bg-[#f9fbff] shadow-[0_14px_40px_-24px_rgba(49,95,189,0.2)] transition-all hover:border-[#0ea5e9]/45 hover:shadow-[0_24px_56px_-24px_rgba(14,165,233,0.26)] dark:border-white/12 dark:bg-[#111b2e]"
                    >
                        <div className="relative aspect-[16/9] overflow-hidden border-b border-slate-200/70 bg-[#f4f7fb] dark:border-white/10">
                            <img
                                src={channelMarketImg}
                                alt={t('highlights.marketTitle')}
                                loading="lazy"
                                className="h-full w-full object-cover transition-transform duration-700 group-hover:scale-105"
                            />
                            <div className="absolute top-4 left-4 inline-flex size-10 items-center justify-center rounded-xl border border-slate-200/90 bg-white/90 text-[#0284c7] shadow-sm backdrop-blur-md dark:border-white/15 dark:bg-[#0f172a]/80 dark:text-sky-300">
                                <Store className="size-5" />
                            </div>
                        </div>
                        <div className="flex flex-1 flex-col justify-between p-6 sm:p-7">
                            <div>
                                <h3 className="text-xl font-semibold tracking-tight text-slate-950 dark:text-white">
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
                                        className="rounded-md border border-sky-500/20 bg-white px-2.5 py-1 text-xs font-medium text-[#0369a1] shadow-2xs dark:border-blue-400/20 dark:bg-blue-400/10 dark:text-blue-200"
                                    >
                                        {tag}
                                    </span>
                                ))}
                            </div>
                        </div>
                    </motion.article>
                </div>

                <div className="mt-8 grid gap-5 sm:grid-cols-2 lg:grid-cols-4">
                    {pillars.map((pillar, idx) => (
                        <motion.div
                            key={pillar.title}
                            initial={{ opacity: 0, y: 16 }}
                            whileInView={{ opacity: 1, y: 0 }}
                            viewport={REVEAL_VIEWPORT}
                            transition={{ duration: 0.4, delay: idx * 0.06 }}
                            className="group rounded-2xl border border-slate-200/85 bg-[#f8faff] p-5 transition-all hover:-translate-y-0.5 hover:border-[#315fbd]/35 hover:bg-white hover:shadow-[0_14px_30px_-20px_rgba(49,95,189,0.28)] dark:border-white/10 dark:bg-[#111b2e]"
                        >
                            <div className="inline-flex size-9 items-center justify-center rounded-lg border border-[#315fbd]/15 bg-[#eef4ff] text-[#315fbd] transition-transform group-hover:scale-105 dark:border-cyan-400/20 dark:bg-cyan-400/10 dark:text-cyan-300">
                                <pillar.icon className="size-4" aria-hidden="true" />
                            </div>
                            <h3 className="mt-4 text-base font-semibold text-slate-950 dark:text-white">
                                {pillar.title}
                            </h3>
                            <p className="mt-2 text-sm leading-relaxed text-slate-600 dark:text-slate-300">
                                {pillar.desc}
                            </p>
                        </motion.div>
                    ))}
                </div>
            </div>
        </section>
    );
}

// MediaSection 接入协议与清算台账: 左侧协议脉冲卡，右侧按模态列出精确计费规则。
function MediaSection() {
    const t = useTranslations('landing');
    const modalities: { icon: LucideIcon; title: string; desc: string; price: string }[] = [
        { icon: MessageSquareText, title: t('media.textTitle'), desc: t('media.textDesc'), price: t('media.textPrice') },
        { icon: ImageIcon, title: t('media.imageTitle'), desc: t('media.imageDesc'), price: t('media.imagePrice') },
        { icon: Video, title: t('media.videoTitle'), desc: t('media.videoDesc'), price: t('media.videoPrice') },
    ];
    return (
        <section id="media" className="scroll-mt-16 border-t border-slate-200/80 dark:border-white/10">
            <div className="mx-auto grid max-w-6xl gap-10 px-4 py-16 md:px-6 md:py-22 lg:grid-cols-12 lg:items-start">
                <motion.div
                    initial={{ opacity: 0, y: 16 }}
                    whileInView={{ opacity: 1, y: 0 }}
                    viewport={REVEAL_VIEWPORT}
                    transition={{ duration: 0.45 }}
                    className="lg:col-span-5"
                >
                    <h2 className="text-2xl font-semibold tracking-tight text-slate-950 dark:text-white md:text-3xl">
                        {t('media.title')}
                    </h2>
                    <p className="mt-3 text-base leading-relaxed text-slate-600 dark:text-slate-300">
                        {t('media.subtitle')}
                    </p>
                    <div className="mt-6 rounded-2xl border border-[#315fbd]/15 bg-white p-5 shadow-[0_12px_32px_-20px_rgba(49,95,189,0.22)] dark:border-white/10 dark:bg-[#111b2e]">
                        <div className="flex items-center gap-2.5 text-sm font-semibold text-slate-950 dark:text-white">
                            <span className="relative flex size-8 items-center justify-center rounded-lg bg-[#eef4ff] text-[#315fbd] dark:bg-cyan-400/15 dark:text-cyan-300">
                                <Radio className="size-4" />
                            </span>
                            <span>{t('media.protocolsTitle')}</span>
                        </div>
                        <p className="mt-3 text-sm leading-relaxed text-slate-600 dark:text-slate-300">
                            {t('media.protocolsDesc')}
                        </p>
                    </div>
                </motion.div>

                <motion.div
                    initial={{ opacity: 0, y: 18 }}
                    whileInView={{ opacity: 1, y: 0 }}
                    viewport={REVEAL_VIEWPORT}
                    transition={{ duration: 0.5, delay: 0.08 }}
                    className="divide-y divide-slate-200/80 overflow-hidden rounded-2xl border border-slate-200/90 bg-white shadow-[0_14px_40px_-24px_rgba(15,23,42,0.14)] dark:divide-white/10 dark:border-white/12 dark:bg-[#111b2e] lg:col-span-7"
                >
                    {modalities.map((item) => (
                        <div
                            key={item.title}
                            className="group relative flex flex-col gap-3 p-5 transition-colors hover:bg-[#f7faff] dark:hover:bg-white/[0.03] sm:flex-row sm:items-start sm:justify-between sm:gap-6 sm:p-6"
                        >
                            <div
                                aria-hidden="true"
                                className="absolute top-3 bottom-3 left-0 w-0.5 rounded-r bg-[#315fbd] opacity-0 transition-opacity group-hover:opacity-100 dark:bg-cyan-400"
                            />
                            <div className="flex items-start gap-3.5">
                                <div className="mt-0.5 flex size-9 shrink-0 items-center justify-center rounded-lg border border-[#315fbd]/15 bg-[#eef4ff] text-[#315fbd] dark:border-cyan-400/20 dark:bg-cyan-400/10 dark:text-cyan-300">
                                    <item.icon className="size-4" />
                                </div>
                                <div>
                                    <h3 className="font-semibold text-slate-950 dark:text-white">{item.title}</h3>
                                    <p className="mt-1 text-sm leading-relaxed text-slate-600 dark:text-slate-300">
                                        {item.desc}
                                    </p>
                                </div>
                            </div>
                            <div className="shrink-0 rounded-lg border border-[#315fbd]/15 bg-[#f0f5ff] px-3 py-1.5 font-mono text-xs font-medium text-[#315fbd] dark:border-cyan-400/20 dark:bg-cyan-400/10 dark:text-cyan-200 sm:text-right">
                                {item.price}
                            </div>
                        </div>
                    ))}
                </motion.div>
            </div>
        </section>
    );
}

// RolesSection 适用角色: 管理员 / 渠道商 / 用户三种视角的权限边界。
function RolesSection() {
    const t = useTranslations('landing');
    const readPoints = (role: 'admin' | 'reseller' | 'user') =>
        ['p1', 'p2', 'p3'].map((key) => t(`rolePoints.${role}.${key}`));
    const roles: { icon: LucideIcon; title: string; desc: string; points: string[]; rim: string }[] = [
        {
            icon: ShieldCheck,
            title: t('roles.adminTitle'),
            desc: t('roles.adminDesc'),
            points: readPoints('admin'),
            rim: 'from-[#315fbd] via-[#0ea5e9]/60 to-transparent',
        },
        {
            icon: Store,
            title: t('roles.resellerTitle'),
            desc: t('roles.resellerDesc'),
            points: readPoints('reseller'),
            rim: 'from-[#0ea5e9] via-indigo-400/60 to-transparent',
        },
        {
            icon: UserRound,
            title: t('roles.userTitle'),
            desc: t('roles.userDesc'),
            points: readPoints('user'),
            rim: 'from-emerald-500 via-teal-400/60 to-transparent',
        },
    ];
    return (
        <section id="roles" className="scroll-mt-16 border-t border-slate-200/80 bg-white dark:border-white/10 dark:bg-[#0c1426]">
            <div className="mx-auto max-w-6xl px-4 py-16 md:px-6 md:py-22">
                <motion.div
                    initial={{ opacity: 0, y: 16 }}
                    whileInView={{ opacity: 1, y: 0 }}
                    viewport={REVEAL_VIEWPORT}
                    transition={{ duration: 0.45 }}
                    className="max-w-2xl"
                >
                    <h2 className="text-2xl font-semibold tracking-tight text-slate-950 dark:text-white md:text-3xl">
                        {t('roles.title')}
                    </h2>
                    <p className="mt-3 text-base leading-relaxed text-slate-600 dark:text-slate-300">
                        {t('roles.subtitle')}
                    </p>
                </motion.div>

                <div className="mt-10 grid gap-6 md:grid-cols-3">
                    {roles.map((role, idx) => (
                        <motion.div
                            key={role.title}
                            initial={{ opacity: 0, y: 18 }}
                            whileInView={{ opacity: 1, y: 0 }}
                            viewport={REVEAL_VIEWPORT}
                            transition={{ duration: 0.45, delay: idx * 0.08 }}
                            className="relative flex flex-col overflow-hidden rounded-2xl border border-slate-200/90 bg-[#f8faff] p-6 transition-all hover:-translate-y-0.5 hover:border-[#315fbd]/35 hover:bg-white hover:shadow-[0_18px_40px_-24px_rgba(49,95,189,0.24)] dark:border-white/12 dark:bg-[#111b2e]"
                        >
                            <div
                                aria-hidden="true"
                                className={`pointer-events-none absolute inset-x-0 top-0 h-[2px] bg-gradient-to-r ${role.rim}`}
                            />
                            <div className="flex items-center gap-3">
                                <div className="flex size-9 items-center justify-center rounded-lg border border-[#315fbd]/15 bg-[#eef4ff] text-[#315fbd] dark:border-cyan-400/20 dark:bg-cyan-400/10 dark:text-cyan-300">
                                    <role.icon className="size-4" />
                                </div>
                                <h3 className="text-base font-semibold text-slate-950 dark:text-white">{role.title}</h3>
                            </div>
                            <p className="mt-3 text-sm leading-relaxed text-slate-600 dark:text-slate-300">{role.desc}</p>
                            <ul className="mt-5 space-y-2.5 border-t border-slate-200/80 pt-4 text-sm text-slate-700 dark:border-white/10 dark:text-slate-200">
                                {role.points.map((point) => (
                                    <li key={point} className="flex items-start gap-2">
                                        <Check className="mt-0.5 size-4 shrink-0 text-[#315fbd] dark:text-cyan-300" aria-hidden="true" />
                                        <span>{point}</span>
                                    </li>
                                ))}
                            </ul>
                        </motion.div>
                    ))}
                </div>
            </div>
        </section>
    );
}

// CtaSection 底部行动区: 冰蓝精密科技感收束带，呼应内页蓝色主调，提供控制台入口与公开状态页直达。
function CtaSection({ onEnter }: { onEnter: () => void }) {
    const t = useTranslations('landing');
    return (
        <section className="border-t border-slate-200/80 dark:border-white/10">
            <div className="mx-auto max-w-6xl px-4 py-16 md:px-6 md:py-20">
                <motion.div
                    initial={{ opacity: 0, y: 20 }}
                    whileInView={{ opacity: 1, y: 0 }}
                    viewport={REVEAL_VIEWPORT}
                    transition={{ duration: 0.5 }}
                    className="relative overflow-hidden rounded-3xl border border-[#315fbd]/20 bg-gradient-to-br from-[#eef4ff] via-white to-[#e6f6ff] px-6 py-10 shadow-[0_24px_60px_-28px_rgba(49,95,189,0.28)] dark:border-cyan-400/25 dark:from-[#0b1730] dark:via-[#081226] dark:to-[#050a16] sm:px-10 sm:py-12"
                >
                    <div aria-hidden="true" className="landing-tech-grid pointer-events-none absolute inset-0 opacity-75" />
                    <div
                        aria-hidden="true"
                        className="pointer-events-none absolute -top-20 right-10 h-64 w-64 rounded-full bg-[#0ea5e9]/15 blur-[80px]"
                    />
                    <div className="relative flex flex-col justify-between gap-8 lg:flex-row lg:items-center">
                        <div className="max-w-xl">
                            <h2 className="text-2xl font-semibold tracking-tight text-slate-950 dark:text-white md:text-3xl">
                                {t('cta.title')}
                            </h2>
                            <p className="mt-2.5 text-sm leading-relaxed text-slate-600 dark:text-slate-300 md:text-base">
                                {t('cta.subtitle')}
                            </p>
                        </div>
                        <div className="flex flex-wrap items-center gap-3">
                            <Button
                                size="lg"
                                className="h-11 rounded-lg bg-gradient-to-r from-[#315fbd] via-[#2563eb] to-[#0ea5e9] px-6 text-sm font-semibold text-white shadow-[0_14px_30px_-10px_rgba(49,95,189,0.6)] hover:brightness-105"
                                onClick={onEnter}
                            >
                                {t('cta.button')}
                                <ArrowRight className="size-4" />
                            </Button>
                            <a
                                href="/status"
                                className="inline-flex h-11 items-center justify-center gap-1.5 rounded-lg border border-slate-200/95 bg-white/90 px-5 text-sm font-medium text-slate-800 backdrop-blur-md transition-colors hover:border-[#315fbd]/40 hover:bg-white dark:border-white/20 dark:bg-white/[0.04] dark:text-white dark:hover:bg-white/10"
                            >
                                {t('cta.statusButton')}
                                <ArrowUpRight className="size-4" />
                            </a>
                        </div>
                    </div>
                </motion.div>
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
        <footer className="border-t border-slate-200/80 bg-[#f7f8fa] dark:border-white/10 dark:bg-[#0b1220]">
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
                            <a className="underline underline-offset-2 hover:text-[#315fbd] dark:hover:text-cyan-300" href={href}>
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

// LandingPage 落地页: 未登录访客的首页, 与内页白底工作台统一主色调，通过亮白精密 3D 配图、动态光路与科技动效展示全站能力。
export function LandingPage({ onEnter }: { onEnter: () => void }) {
    const { data: site } = useSiteConfig();
    const siteName = site?.site_name?.trim() || DEFAULT_SITE_NAME;
    return (
        <MotionConfig reducedMotion="user">
            <div className="min-h-dvh scroll-smooth bg-[#f7f8fa] text-slate-900 antialiased selection:bg-[#315fbd]/15 selection:text-[#315fbd] dark:bg-[#0b1220] dark:text-slate-100">
                <LandingHeader siteName={siteName} onEnter={onEnter} />
                <main>
                    <HeroSection onEnter={onEnter} />
                    <CoreShowcaseMetrics />
                    <FlowSection />
                    <FeaturesSection />
                    <MediaSection />
                    <RolesSection />
                    <CtaSection onEnter={onEnter} />
                </main>
                <LandingFooter siteName={siteName} site={site} />
            </div>
        </MotionConfig>
    );
}
