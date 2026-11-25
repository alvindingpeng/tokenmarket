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

// LandingHeader 顶部吸顶栏: 深空玻璃质感，保留品牌、锚点导航、语言切换与控制台入口。
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
        <header className="sticky top-0 z-50 border-b border-white/10 bg-[#030712]/80 backdrop-blur-xl">
            <div className="mx-auto flex min-h-16 max-w-6xl flex-wrap items-center justify-between gap-3 px-4 py-2.5 md:flex-nowrap md:px-6 md:py-0">
                <a href="#top" className="group flex items-center gap-2.5 text-white">
                    <span className="relative flex items-center justify-center rounded-lg p-0.5 ring-1 ring-cyan-400/30 transition-all group-hover:ring-cyan-300/60">
                        <Logo size={30} />
                    </span>
                    <span className="text-base font-semibold tracking-tight md:text-lg">{siteName}</span>
                </a>
                <nav className="hidden items-center gap-1 md:flex" aria-label="Main">
                    {links.map((link) => (
                        <a
                            key={link.href}
                            href={link.href}
                            className="rounded-lg px-3 py-1.5 text-sm text-slate-300 transition-colors hover:bg-white/[0.06] hover:text-cyan-200"
                        >
                            {link.label}
                        </a>
                    ))}
                </nav>
                <div className="flex w-full items-center justify-between gap-2 sm:w-auto sm:justify-end">
                    <a
                        href="/status"
                        className="inline-flex min-h-10 items-center gap-1.5 text-sm font-medium text-cyan-300 hover:text-cyan-200 md:hidden"
                    >
                        <span className="size-2 rounded-full bg-emerald-400 shadow-[0_0_8px_#34d399]" />
                        {t('nav.status')}
                    </a>
                    <div className="flex items-center gap-2">
                        <LanguageSwitch />
                        <Button
                            size="sm"
                            className="rounded-lg bg-gradient-to-r from-cyan-400 to-blue-500 px-4 font-semibold text-slate-950 shadow-[0_0_24px_-4px_rgba(34,211,238,0.6)] transition-all hover:brightness-110"
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

// RoutingTopologyPulse 调度台顶部动态光路拓扑: 可视化呈现协议入站、核心路由与主备热切光路。
function RoutingTopologyPulse({ scenario }: { scenario: WorkbenchScenarioID }) {
    const accent =
        scenario === 'coding' ? '#22d3ee' : scenario === 'summary' ? '#60a5fa' : '#a78bfa';

    return (
        <div className="relative overflow-hidden border-b border-white/10 bg-[#040a18] px-4 py-3 sm:px-5">
            <svg
                viewBox="0 0 420 56"
                className="h-12 w-full"
                fill="none"
                aria-hidden="true"
            >
                <defs>
                    <linearGradient id="route-active-grad" x1="0%" y1="0%" x2="100%" y2="0%">
                        <stop offset="0%" stopColor={accent} stopOpacity="0.25" />
                        <stop offset="50%" stopColor={accent} stopOpacity="1" />
                        <stop offset="100%" stopColor="#34d399" stopOpacity="1" />
                    </linearGradient>
                </defs>

                {/* Base tracks */}
                <path
                    d="M 28 28 L 170 28 C 215 28, 225 14, 270 14 L 392 14"
                    stroke="rgba(148,163,184,0.22)"
                    strokeWidth="2"
                />
                <path
                    d="M 170 28 C 215 28, 225 42, 270 42 L 392 42"
                    stroke="rgba(148,163,184,0.16)"
                    strokeWidth="1.5"
                    strokeDasharray="4 4"
                />

                {/* Animated luminous packet stream on active route */}
                <path
                    d="M 28 28 L 170 28 C 215 28, 225 14, 270 14 L 392 14"
                    stroke="url(#route-active-grad)"
                    strokeWidth="2.5"
                    strokeLinecap="round"
                    className="landing-packet-animate"
                />

                {/* Inbound node */}
                <circle cx="28" cy="28" r="5" fill="#0f172a" stroke={accent} strokeWidth="2" />
                <circle cx="28" cy="28" r="2" fill={accent} />

                {/* Router Core node */}
                <rect
                    x="156"
                    y="15"
                    width="26"
                    height="26"
                    rx="7"
                    fill="#0b1528"
                    stroke={accent}
                    strokeWidth="1.6"
                />
                <circle cx="169" cy="28" r="4.5" fill={accent} opacity="0.9" />

                {/* Primary active target node */}
                <circle cx="392" cy="14" r="5.5" fill="#064e3b" stroke="#34d399" strokeWidth="2" />
                <circle cx="392" cy="14" r="2.2" fill="#34d399" />

                {/* Standby target node */}
                <circle cx="392" cy="42" r="4.5" fill="#0f172a" stroke="rgba(148,163,184,0.45)" strokeWidth="1.5" />
            </svg>
        </div>
    );
}

// SmartRoutingWorkbench 首屏交互式编组调度台: 访客可切换真实调用场景，查看实时光路、评分选路、热备切道与账本结算。
function SmartRoutingWorkbench() {
    const t = useTranslations('landing');
    const [active, setActive] = useState<WorkbenchScenarioID>('coding');
    const prefix = `workbench.scenarios.${active}`;

    return (
        <div className="relative overflow-hidden rounded-2xl border border-cyan-400/25 bg-[#081022]/90 shadow-[0_32px_90px_-24px_rgba(6,182,212,0.35)] backdrop-blur-xl">
            {/* Subtle top luminous rim */}
            <div
                aria-hidden="true"
                className="pointer-events-none absolute inset-x-0 top-0 h-px bg-gradient-to-r from-transparent via-cyan-400/80 to-transparent"
            />

            <div className="flex flex-wrap items-center justify-between gap-3 border-b border-white/10 px-4 py-3.5 sm:px-5">
                <div className="flex items-center gap-2 text-xs font-medium text-cyan-200">
                    <span className="relative flex size-2.5">
                        <span className="absolute inline-flex size-full animate-ping rounded-full bg-emerald-400 opacity-75" />
                        <span className="relative inline-flex size-2.5 rounded-full bg-emerald-400" />
                    </span>
                    <Sparkles className="size-3.5 text-cyan-300" aria-hidden="true" />
                    <span>{t('workbench.heading')}</span>
                    <span className="hidden rounded border border-emerald-400/25 bg-emerald-400/10 px-1.5 py-0.5 text-[10px] font-medium text-emerald-300 sm:inline-block">
                        {t('workbench.statusLive')}
                    </span>
                </div>
                <div
                    role="tablist"
                    aria-label={t('workbench.heading')}
                    className="inline-flex rounded-lg border border-white/10 bg-white/[0.04] p-0.5"
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
                                    selected ? 'text-slate-950' : 'text-slate-300 hover:text-white'
                                }`}
                            >
                                {selected && (
                                    <motion.span
                                        layoutId="landing-workbench-pill"
                                        transition={{ type: 'spring', stiffness: 420, damping: 32 }}
                                        className="absolute inset-0 rounded-md bg-gradient-to-r from-cyan-300 to-blue-400 shadow-[0_0_16px_rgba(34,211,238,0.45)]"
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
                    className="divide-y divide-white/10"
                >
                    <div className="px-4 py-3.5 sm:px-5">
                        <div className="flex flex-wrap items-center justify-between gap-2">
                            <span className="text-xs font-medium text-slate-400">
                                {t('workbench.inboundLabel')}
                            </span>
                            <code className="rounded border border-cyan-400/20 bg-cyan-400/10 px-2 py-0.5 font-mono text-xs text-cyan-200">
                                {t(`${prefix}.endpoint`)}
                            </code>
                        </div>
                        <div className="mt-2 flex flex-wrap items-baseline justify-between gap-2">
                            <span className="text-sm font-semibold text-white">
                                {t(`${prefix}.protocol`)}
                            </span>
                            <span className="font-mono text-xs text-cyan-300">
                                {t(`${prefix}.model`)}
                            </span>
                        </div>
                    </div>

                    <div className="px-4 py-3.5 sm:px-5">
                        <div className="text-xs font-medium text-slate-400">
                            {t('workbench.routingLabel')}
                        </div>
                        <p className="mt-1.5 text-sm leading-relaxed text-slate-200">
                            {t(`${prefix}.policy`)}
                        </p>
                    </div>

                    <div className="space-y-2.5 px-4 py-3.5 sm:px-5">
                        <div className="text-xs font-medium text-slate-400">
                            {t('workbench.tracksLabel')}
                        </div>
                        <div className="flex items-start gap-2.5 rounded-xl border border-emerald-400/30 bg-emerald-400/[0.08] px-3 py-2.5 shadow-[inset_0_0_24px_rgba(16,185,129,0.08)]">
                            <span className="mt-0.5 shrink-0 rounded bg-emerald-400 px-1.5 py-0.5 text-[11px] font-semibold text-slate-950">
                                {t('workbench.primaryBadge')}
                            </span>
                            <span className="text-xs leading-relaxed text-slate-100">
                                {t(`${prefix}.trackA`)}
                            </span>
                        </div>
                        <div className="flex items-start gap-2.5 rounded-xl border border-white/10 bg-white/[0.03] px-3 py-2.5">
                            <span className="mt-0.5 shrink-0 rounded bg-white/12 px-1.5 py-0.5 text-[11px] font-medium text-slate-300">
                                {t('workbench.standbyBadge')}
                            </span>
                            <span className="text-xs leading-relaxed text-slate-300">
                                {t(`${prefix}.trackB`)}
                            </span>
                        </div>
                    </div>

                    <div className="bg-[#050b18]/90 px-4 py-3.5 sm:px-5">
                        <div className="text-xs font-medium text-slate-400">
                            {t('workbench.ledgerLabel')}
                        </div>
                        <p className="mt-1 text-xs leading-relaxed text-cyan-100/90">
                            {t(`${prefix}.ledger`)}
                        </p>
                    </div>
                </motion.div>
            </AnimatePresence>
        </div>
    );
}

// HeroSection 首屏: 深空网格 + 呼吸光晕 + 左侧渐变主标题与入口，右侧实时光路调度台。
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
            {/* Perspective tech grid & animated plasma orbs */}
            <div aria-hidden="true" className="landing-tech-grid pointer-events-none absolute inset-0" />
            <div
                aria-hidden="true"
                className="landing-orb-animate pointer-events-none absolute -top-24 left-1/4 h-[30rem] w-[30rem] -translate-x-1/2 rounded-full bg-cyan-500/20 blur-[130px]"
            />
            <div
                aria-hidden="true"
                className="landing-orb-animate pointer-events-none absolute top-16 right-0 h-[28rem] w-[28rem] rounded-full bg-blue-600/20 blur-[130px]"
                style={{ animationDelay: '2.5s' }}
            />

            <div className="relative mx-auto grid max-w-6xl gap-10 px-4 pt-12 pb-12 md:px-6 md:pt-20 md:pb-16 lg:grid-cols-12 lg:items-center lg:gap-12">
                <motion.div
                    initial={{ opacity: 0, y: 18 }}
                    animate={{ opacity: 1, y: 0 }}
                    transition={{ duration: 0.55, ease: 'easeOut' }}
                    className="lg:col-span-6"
                >
                    <div className="inline-flex items-center gap-2 rounded-full border border-cyan-400/30 bg-cyan-400/10 px-3.5 py-1 text-xs font-medium text-cyan-200 backdrop-blur-md">
                        <Zap className="size-3.5 text-cyan-300" aria-hidden="true" />
                        <span>{t('hero.badge')}</span>
                    </div>

                    <h1 className="mt-5 max-w-xl bg-gradient-to-br from-white via-slate-100 to-cyan-200 bg-clip-text text-3xl font-semibold tracking-[-0.03em] text-transparent sm:text-4xl lg:text-[3.2rem] lg:leading-[1.12]">
                        {t('hero.title')}
                    </h1>
                    <p className="mt-5 max-w-xl text-base leading-relaxed text-slate-300 md:text-lg">
                        {subtitle}
                    </p>

                    <div className="mt-8 flex flex-wrap items-center gap-3">
                        <Button
                            size="lg"
                            className="h-11 rounded-lg bg-gradient-to-r from-cyan-400 via-sky-400 to-blue-500 px-6 text-sm font-semibold text-slate-950 shadow-[0_0_32px_-4px_rgba(34,211,238,0.65)] transition-all hover:scale-[1.01] hover:brightness-110"
                            onClick={onEnter}
                        >
                            {t('hero.ctaPrimary')}
                            <ArrowRight className="size-4" />
                        </Button>
                        <a
                            href="/status"
                            className="inline-flex h-11 items-center justify-center gap-2 rounded-lg border border-white/15 bg-white/[0.05] px-5 text-sm font-medium text-slate-100 backdrop-blur-md transition-colors hover:border-cyan-400/40 hover:bg-white/10"
                        >
                            <Activity className="size-4 text-emerald-400" />
                            {t('cta.statusButton')}
                        </a>
                    </div>

                    <div className="mt-7 flex flex-wrap gap-2">
                        {chips.map((chip) => (
                            <span
                                key={chip}
                                className="rounded-md border border-white/10 bg-white/[0.03] px-2.5 py-1 text-xs text-slate-300"
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

// CoreShowcaseMetrics 光子编组核心视觉展板 + 架构规格带: 以 3D 光子核心配图直呈现汇入、编组与并行输出，并在底部悬浮展示 4 项硬核指标。
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
                className="relative mx-auto max-w-6xl overflow-hidden rounded-3xl border border-cyan-400/20 bg-[#050b18] shadow-[0_32px_90px_-30px_rgba(34,211,238,0.28)]"
            >
                {/* Top sweeping laser beam */}
                <div aria-hidden="true" className="pointer-events-none absolute inset-x-0 top-0 h-[2px] overflow-hidden">
                    <div className="landing-beam-animate h-full w-1/3 bg-gradient-to-r from-transparent via-cyan-300 to-transparent" />
                </div>

                <div className="relative aspect-[16/9] max-h-[420px] w-full overflow-hidden bg-[#020611] sm:aspect-[21/9]">
                    <img
                        src={heroCoreImg}
                        alt={t('hero.badge')}
                        className="h-full w-full object-cover object-center opacity-95 transition-transform duration-1000 hover:scale-[1.03]"
                    />
                    {/* Animated vertical scanline sheen */}
                    <div
                        aria-hidden="true"
                        className="landing-scanline-animate pointer-events-none absolute inset-x-0 top-0 h-24 bg-gradient-to-b from-transparent via-cyan-300/12 to-transparent"
                    />
                    {/* Soft vignette to blend into metrics dock */}
                    <div
                        aria-hidden="true"
                        className="pointer-events-none absolute inset-0 bg-gradient-to-t from-[#050b18] via-transparent to-[#050b18]/30"
                    />
                </div>

                <div className="relative grid grid-cols-2 divide-y divide-white/10 border-t border-white/10 bg-[#071024]/85 backdrop-blur-md sm:divide-y-0 lg:grid-cols-4 lg:divide-x">
                    {items.map((item, idx) => (
                        <motion.div
                            key={item.label}
                            initial={{ opacity: 0, y: 12 }}
                            whileInView={{ opacity: 1, y: 0 }}
                            viewport={REVEAL_VIEWPORT}
                            transition={{ duration: 0.4, delay: idx * 0.06 }}
                            className="px-4 py-5 sm:px-6 lg:py-6"
                        >
                            <div className="bg-gradient-to-r from-white to-cyan-200 bg-clip-text text-xl font-semibold tracking-tight text-transparent tabular-nums md:text-2xl">
                                {item.value}
                            </div>
                            <div className="mt-1 text-xs leading-relaxed text-slate-400 sm:text-sm">
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
        <section id="flow" className="relative scroll-mt-16 border-t border-white/10">
            <div className="mx-auto max-w-6xl px-4 py-16 md:px-6 md:py-22">
                <motion.div
                    initial={{ opacity: 0, y: 16 }}
                    whileInView={{ opacity: 1, y: 0 }}
                    viewport={REVEAL_VIEWPORT}
                    transition={{ duration: 0.45 }}
                    className="max-w-2xl"
                >
                    <h2 className="text-2xl font-semibold tracking-tight text-white md:text-3xl">
                        {t('flow.title')}
                    </h2>
                    <p className="mt-3 text-base leading-relaxed text-slate-300">
                        {t('flow.subtitle')}
                    </p>
                </motion.div>

                <div className="relative mt-12">
                    {/* Desktop animated conduit line */}
                    <div
                        aria-hidden="true"
                        className="pointer-events-none absolute top-0 right-0 left-0 hidden h-[2px] overflow-hidden bg-white/10 lg:block"
                    >
                        <div className="landing-beam-animate h-full w-1/3 bg-gradient-to-r from-transparent via-cyan-400 to-transparent" />
                    </div>

                    <ol className="grid gap-5 sm:grid-cols-2 lg:grid-cols-4">
                        {steps.map((step, index) => (
                            <motion.li
                                key={step.title}
                                initial={{ opacity: 0, y: 18 }}
                                whileInView={{ opacity: 1, y: 0 }}
                                viewport={REVEAL_VIEWPORT}
                                transition={{ duration: 0.45, delay: index * 0.08 }}
                                className="group relative flex flex-col rounded-2xl border border-white/10 bg-white/[0.025] p-5 backdrop-blur-md transition-all hover:border-cyan-400/40 hover:bg-white/[0.045]"
                            >
                                <div className="flex items-center justify-between">
                                    <span className="rounded-md border border-cyan-400/25 bg-cyan-400/10 px-2 py-0.5 font-mono text-xs font-semibold text-cyan-300 tabular-nums">
                                        {String(index + 1).padStart(2, '0')}
                                    </span>
                                    <step.icon
                                        className="size-4 text-slate-400 transition-colors group-hover:text-cyan-300"
                                        aria-hidden="true"
                                    />
                                </div>
                                <h3 className="mt-4 text-base font-semibold text-white">{step.title}</h3>
                                <p className="mt-2 text-sm leading-relaxed text-slate-300">{step.desc}</p>
                            </motion.li>
                        ))}
                    </ol>
                </div>
            </div>
        </section>
    );
}

// FeaturesSection 核心能力: 顶部两张 3D 科技配图大卡突出智能路由与模型市场，下方四列核心支柱卡片。
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
        <section id="features" className="relative scroll-mt-16 border-t border-white/10 bg-[#050a16]">
            <div
                aria-hidden="true"
                className="pointer-events-none absolute inset-x-0 top-0 h-px bg-gradient-to-r from-transparent via-blue-400/40 to-transparent"
            />
            <div className="mx-auto max-w-6xl px-4 py-16 md:px-6 md:py-22">
                <motion.div
                    initial={{ opacity: 0, y: 16 }}
                    whileInView={{ opacity: 1, y: 0 }}
                    viewport={REVEAL_VIEWPORT}
                    transition={{ duration: 0.45 }}
                    className="max-w-2xl"
                >
                    <h2 className="text-2xl font-semibold tracking-tight text-white md:text-3xl">
                        {t('features.title')}
                    </h2>
                    <p className="mt-3 text-base leading-relaxed text-slate-300">
                        {t('features.subtitle')}
                    </p>
                </motion.div>

                <div className="mt-10 grid gap-6 lg:grid-cols-2">
                    {/* Card 1: Smart Routing with 3D Visual */}
                    <motion.article
                        initial={{ opacity: 0, y: 20 }}
                        whileInView={{ opacity: 1, y: 0 }}
                        viewport={REVEAL_VIEWPORT}
                        transition={{ duration: 0.5 }}
                        className="group flex flex-col overflow-hidden rounded-2xl border border-white/12 bg-[#081124] transition-all hover:border-cyan-400/45 hover:shadow-[0_24px_70px_-24px_rgba(34,211,238,0.3)]"
                    >
                        <div className="relative aspect-[16/9] overflow-hidden bg-[#040914]">
                            <img
                                src={smartRoutingImg}
                                alt={t('highlights.smartRoutingTitle')}
                                loading="lazy"
                                className="h-full w-full object-cover transition-transform duration-700 group-hover:scale-105"
                            />
                            <div
                                aria-hidden="true"
                                className="pointer-events-none absolute inset-0 bg-gradient-to-t from-[#081124] via-[#081124]/20 to-transparent"
                            />
                            <div className="absolute top-4 left-4 inline-flex size-10 items-center justify-center rounded-xl border border-cyan-300/30 bg-[#040914]/75 text-cyan-300 backdrop-blur-md">
                                <FolderTree className="size-5" />
                            </div>
                        </div>
                        <div className="flex flex-1 flex-col justify-between p-6 sm:p-7">
                            <div>
                                <h3 className="text-xl font-semibold tracking-tight text-white">
                                    {t('highlights.smartRoutingTitle')}
                                </h3>
                                <p className="mt-3 text-sm leading-relaxed text-slate-300">
                                    {t('highlights.smartRoutingDesc')}
                                </p>
                            </div>
                            <div className="mt-6 flex flex-wrap gap-2">
                                {routingTags.map((tag) => (
                                    <span
                                        key={tag}
                                        className="rounded-md border border-cyan-400/25 bg-cyan-400/10 px-2.5 py-1 text-xs font-medium text-cyan-200"
                                    >
                                        {tag}
                                    </span>
                                ))}
                            </div>
                        </div>
                    </motion.article>

                    {/* Card 2: Channel Marketplace & Settlement with 3D Visual */}
                    <motion.article
                        initial={{ opacity: 0, y: 20 }}
                        whileInView={{ opacity: 1, y: 0 }}
                        viewport={REVEAL_VIEWPORT}
                        transition={{ duration: 0.5, delay: 0.1 }}
                        className="group flex flex-col overflow-hidden rounded-2xl border border-white/12 bg-[#081124] transition-all hover:border-blue-400/45 hover:shadow-[0_24px_70px_-24px_rgba(59,130,246,0.3)]"
                    >
                        <div className="relative aspect-[16/9] overflow-hidden bg-[#040914]">
                            <img
                                src={channelMarketImg}
                                alt={t('highlights.marketTitle')}
                                loading="lazy"
                                className="h-full w-full object-cover transition-transform duration-700 group-hover:scale-105"
                            />
                            <div
                                aria-hidden="true"
                                className="pointer-events-none absolute inset-0 bg-gradient-to-t from-[#081124] via-[#081124]/20 to-transparent"
                            />
                            <div className="absolute top-4 left-4 inline-flex size-10 items-center justify-center rounded-xl border border-amber-300/30 bg-[#040914]/75 text-amber-300 backdrop-blur-md">
                                <Store className="size-5" />
                            </div>
                        </div>
                        <div className="flex flex-1 flex-col justify-between p-6 sm:p-7">
                            <div>
                                <h3 className="text-xl font-semibold tracking-tight text-white">
                                    {t('highlights.marketTitle')}
                                </h3>
                                <p className="mt-3 text-sm leading-relaxed text-slate-300">
                                    {t('highlights.marketDesc')}
                                </p>
                            </div>
                            <div className="mt-6 flex flex-wrap gap-2">
                                {marketTags.map((tag) => (
                                    <span
                                        key={tag}
                                        className="rounded-md border border-blue-400/25 bg-blue-400/10 px-2.5 py-1 text-xs font-medium text-blue-200"
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
                            className="group rounded-2xl border border-white/10 bg-white/[0.025] p-5 transition-all hover:border-cyan-400/35 hover:bg-white/[0.045]"
                        >
                            <div className="inline-flex size-9 items-center justify-center rounded-lg border border-cyan-400/20 bg-cyan-400/10 text-cyan-300 transition-transform group-hover:scale-105">
                                <pillar.icon className="size-4" aria-hidden="true" />
                            </div>
                            <h3 className="mt-4 text-base font-semibold text-white">
                                {pillar.title}
                            </h3>
                            <p className="mt-2 text-sm leading-relaxed text-slate-300">
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
        <section id="media" className="scroll-mt-16 border-t border-white/10">
            <div className="mx-auto grid max-w-6xl gap-10 px-4 py-16 md:px-6 md:py-22 lg:grid-cols-12 lg:items-start">
                <motion.div
                    initial={{ opacity: 0, y: 16 }}
                    whileInView={{ opacity: 1, y: 0 }}
                    viewport={REVEAL_VIEWPORT}
                    transition={{ duration: 0.45 }}
                    className="lg:col-span-5"
                >
                    <h2 className="text-2xl font-semibold tracking-tight text-white md:text-3xl">
                        {t('media.title')}
                    </h2>
                    <p className="mt-3 text-base leading-relaxed text-slate-300">
                        {t('media.subtitle')}
                    </p>
                    <div className="mt-6 rounded-2xl border border-cyan-400/20 bg-[#081124] p-5 shadow-[inset_0_0_32px_rgba(34,211,238,0.06)]">
                        <div className="flex items-center gap-2.5 text-sm font-semibold text-white">
                            <span className="relative flex size-8 items-center justify-center rounded-lg bg-cyan-400/15 text-cyan-300">
                                <Radio className="size-4" />
                            </span>
                            <span>{t('media.protocolsTitle')}</span>
                        </div>
                        <p className="mt-3 text-sm leading-relaxed text-slate-300">
                            {t('media.protocolsDesc')}
                        </p>
                    </div>
                </motion.div>

                <motion.div
                    initial={{ opacity: 0, y: 18 }}
                    whileInView={{ opacity: 1, y: 0 }}
                    viewport={REVEAL_VIEWPORT}
                    transition={{ duration: 0.5, delay: 0.08 }}
                    className="divide-y divide-white/10 overflow-hidden rounded-2xl border border-white/12 bg-[#081124] lg:col-span-7"
                >
                    {modalities.map((item) => (
                        <div
                            key={item.title}
                            className="group relative flex flex-col gap-3 p-5 transition-colors hover:bg-white/[0.03] sm:flex-row sm:items-start sm:justify-between sm:gap-6 sm:p-6"
                        >
                            <div
                                aria-hidden="true"
                                className="absolute top-3 bottom-3 left-0 w-0.5 rounded-r bg-cyan-400 opacity-0 transition-opacity group-hover:opacity-100"
                            />
                            <div className="flex items-start gap-3.5">
                                <div className="mt-0.5 flex size-9 shrink-0 items-center justify-center rounded-lg border border-cyan-400/20 bg-cyan-400/10 text-cyan-300">
                                    <item.icon className="size-4" />
                                </div>
                                <div>
                                    <h3 className="font-semibold text-white">{item.title}</h3>
                                    <p className="mt-1 text-sm leading-relaxed text-slate-300">
                                        {item.desc}
                                    </p>
                                </div>
                            </div>
                            <div className="shrink-0 rounded-lg border border-cyan-400/20 bg-cyan-400/10 px-3 py-1.5 font-mono text-xs font-medium text-cyan-200 sm:text-right">
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
            rim: 'from-cyan-400/60 via-cyan-400/10 to-transparent',
        },
        {
            icon: Store,
            title: t('roles.resellerTitle'),
            desc: t('roles.resellerDesc'),
            points: readPoints('reseller'),
            rim: 'from-blue-400/60 via-blue-400/10 to-transparent',
        },
        {
            icon: UserRound,
            title: t('roles.userTitle'),
            desc: t('roles.userDesc'),
            points: readPoints('user'),
            rim: 'from-emerald-400/60 via-emerald-400/10 to-transparent',
        },
    ];
    return (
        <section id="roles" className="scroll-mt-16 border-t border-white/10 bg-[#050a16]">
            <div className="mx-auto max-w-6xl px-4 py-16 md:px-6 md:py-22">
                <motion.div
                    initial={{ opacity: 0, y: 16 }}
                    whileInView={{ opacity: 1, y: 0 }}
                    viewport={REVEAL_VIEWPORT}
                    transition={{ duration: 0.45 }}
                    className="max-w-2xl"
                >
                    <h2 className="text-2xl font-semibold tracking-tight text-white md:text-3xl">
                        {t('roles.title')}
                    </h2>
                    <p className="mt-3 text-base leading-relaxed text-slate-300">
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
                            className="relative flex flex-col overflow-hidden rounded-2xl border border-white/12 bg-[#081124] p-6 transition-all hover:border-cyan-400/35"
                        >
                            <div
                                aria-hidden="true"
                                className={`pointer-events-none absolute inset-x-0 top-0 h-px bg-gradient-to-r ${role.rim}`}
                            />
                            <div className="flex items-center gap-3">
                                <div className="flex size-9 items-center justify-center rounded-lg border border-cyan-400/20 bg-cyan-400/10 text-cyan-300">
                                    <role.icon className="size-4" />
                                </div>
                                <h3 className="text-base font-semibold text-white">{role.title}</h3>
                            </div>
                            <p className="mt-3 text-sm leading-relaxed text-slate-300">{role.desc}</p>
                            <ul className="mt-5 space-y-2.5 border-t border-white/10 pt-4 text-sm text-slate-200">
                                {role.points.map((point) => (
                                    <li key={point} className="flex items-start gap-2">
                                        <Check className="mt-0.5 size-4 shrink-0 text-cyan-300" aria-hidden="true" />
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

// CtaSection 底部行动区: 高能极光科技感收束带，提供控制台入口与公开状态页直达。
function CtaSection({ onEnter }: { onEnter: () => void }) {
    const t = useTranslations('landing');
    return (
        <section className="border-t border-white/10">
            <div className="mx-auto max-w-6xl px-4 py-16 md:px-6 md:py-20">
                <motion.div
                    initial={{ opacity: 0, y: 20 }}
                    whileInView={{ opacity: 1, y: 0 }}
                    viewport={REVEAL_VIEWPORT}
                    transition={{ duration: 0.5 }}
                    className="relative overflow-hidden rounded-3xl border border-cyan-400/25 bg-gradient-to-br from-[#0b1730] via-[#081226] to-[#050a16] px-6 py-10 text-white shadow-[0_32px_90px_-30px_rgba(34,211,238,0.3)] sm:px-10 sm:py-12"
                >
                    <div aria-hidden="true" className="landing-tech-grid pointer-events-none absolute inset-0 opacity-60" />
                    <div
                        aria-hidden="true"
                        className="pointer-events-none absolute -top-20 right-10 h-64 w-64 rounded-full bg-cyan-400/20 blur-[90px]"
                    />
                    <div className="relative flex flex-col justify-between gap-8 lg:flex-row lg:items-center">
                        <div className="max-w-xl">
                            <h2 className="text-2xl font-semibold tracking-tight md:text-3xl">{t('cta.title')}</h2>
                            <p className="mt-2.5 text-sm leading-relaxed text-slate-300 md:text-base">{t('cta.subtitle')}</p>
                        </div>
                        <div className="flex flex-wrap items-center gap-3">
                            <Button
                                size="lg"
                                className="h-11 rounded-lg bg-gradient-to-r from-cyan-400 via-sky-400 to-blue-500 px-6 text-sm font-semibold text-slate-950 shadow-[0_0_28px_-4px_rgba(34,211,238,0.65)] hover:brightness-110"
                                onClick={onEnter}
                            >
                                {t('cta.button')}
                                <ArrowRight className="size-4" />
                            </Button>
                            <a
                                href="/status"
                                className="inline-flex h-11 items-center justify-center gap-1.5 rounded-lg border border-white/20 bg-white/[0.04] px-5 text-sm font-medium text-white backdrop-blur-md transition-colors hover:border-cyan-400/40 hover:bg-white/10"
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
        <footer className="border-t border-white/10 bg-[#030712]">
            <div className="mx-auto flex max-w-6xl flex-col items-start justify-between gap-4 px-4 py-10 sm:flex-row sm:items-center md:px-6">
                <div className="space-y-1">
                    <div className="flex items-center gap-2.5 text-white">
                        <Logo size={24} />
                        <span className="text-sm font-semibold">{siteName}</span>
                    </div>
                    <p className="text-xs text-slate-400">{t('footer.tagline')}</p>
                </div>
                <div className="flex flex-wrap items-center gap-4 text-xs text-slate-400">
                    <span className="inline-flex items-center gap-1.5">
                        <Languages className="size-3.5" aria-hidden="true" />
                        {t('hero.chip3')}
                    </span>
                    {contact &&
                        (href ? (
                            <a className="underline underline-offset-2 hover:text-cyan-300" href={href}>
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

// LandingPage 落地页: 未登录访客的首页, 以深空科技美学、3D 配图与动态光路展示全站功能并将入口导向控制台。
export function LandingPage({ onEnter }: { onEnter: () => void }) {
    const { data: site } = useSiteConfig();
    const siteName = site?.site_name?.trim() || DEFAULT_SITE_NAME;
    return (
        <MotionConfig reducedMotion="user">
            <div className="dark min-h-dvh scroll-smooth bg-[#030712] text-slate-100 antialiased selection:bg-cyan-400/30 selection:text-cyan-100">
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
