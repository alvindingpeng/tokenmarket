'use client';

import { useMemo, useState, useEffect } from 'react';
import { Clock, Cpu, Zap, AlertCircle, ArrowDownToLine, ArrowUpFromLine, DollarSign, ArrowRight, Send, MessageSquare } from 'lucide-react';
import { useTranslations } from 'next-intl';
import { motion, AnimatePresence } from 'motion/react';
import JsonView from '@uiw/react-json-view';
import { githubDarkTheme } from '@uiw/react-json-view/githubDark';
import { githubLightTheme } from '@uiw/react-json-view/githubLight';
import { useTheme } from 'next-themes';
import { type RelayLog } from '@/api/endpoints/log';
import { getModelIcon } from '@/lib/model-icons';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import {
    MorphingDialog,
    MorphingDialogTrigger,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogClose,
    MorphingDialogTitle,
    MorphingDialogDescription,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';

/**
 * 格式化时间戳
 */
function formatTime(timestamp: number): string {
    const date = new Date(timestamp * 1000);
    return date.toLocaleString('zh-CN', {
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
    });
}

/**
 * 格式化毫秒为可读时间
 */
function formatDuration(ms: number): string {
    if (ms < 1000) return `${ms}ms`;
    return `${(ms / 1000).toFixed(2)}s`;
}

/**
 * 安全解析 JSON 字符串
 */
function safeParseJson(content: string | undefined): { isJson: boolean; data: unknown } {
    if (!content) return { isJson: false, data: null };
    try {
        const parsed = JSON.parse(content);
        return { isJson: true, data: parsed };
    } catch {
        return { isJson: false, data: content };
    }
}

/**
 * JSON 内容渲染组件 - 支持延迟渲染和过渡动画
 */
function JsonContent({
    content,
    fallbackText,
    shouldRender = true
}: {
    content: string | undefined;
    fallbackText: string;
    shouldRender?: boolean;
}) {
    const { resolvedTheme } = useTheme();
    const { isJson, data } = useMemo(() => safeParseJson(content), [content]);

    if (!content) {
        return (
            <pre className="p-4 text-xs text-muted-foreground whitespace-pre-wrap break-words font-mono leading-relaxed">
                {fallbackText}
            </pre>
        );
    }

    return (
        <AnimatePresence mode="wait">
            {!shouldRender ? (
                <motion.div
                    key="loading"
                    initial={{ opacity: 0 }}
                    animate={{ opacity: 1 }}
                    exit={{ opacity: 0 }}
                    transition={{ duration: 0.15 }}
                    className="p-4 flex items-center justify-center h-full"
                >
                    <div className="text-xs text-muted-foreground animate-pulse">Loading...</div>
                </motion.div>
            ) : isJson ? (
                <motion.div
                    key="json"
                    initial={{ opacity: 0 }}
                    animate={{ opacity: 1 }}
                    exit={{ opacity: 0 }}
                    transition={{ duration: 0.2 }}
                    className="p-4"
                >
                    <JsonView
                        value={data as object}
                        style={{
                            ...(resolvedTheme === 'dark' ? githubDarkTheme : githubLightTheme),
                            fontSize: '12px',
                            fontFamily: 'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace',
                            backgroundColor: 'transparent',
                        }}
                        displayDataTypes={false}
                        displayObjectSize={false}
                        enableClipboard={false}
                        collapsed={false}
                    />
                </motion.div>
            ) : (
                <motion.pre
                    key="text"
                    initial={{ opacity: 0 }}
                    animate={{ opacity: 1 }}
                    exit={{ opacity: 0 }}
                    transition={{ duration: 0.2 }}
                    className="p-4 text-xs text-muted-foreground whitespace-pre-wrap break-words font-mono leading-relaxed"
                >
                    {content}
                </motion.pre>
            )}
        </AnimatePresence>
    );
}

/**
 * 延迟渲染的 JSON 内容区域 - 等待对话框展开动画完成后再渲染
 */
function DeferredJsonContent({
    content,
    fallbackText
}: {
    content: string | undefined;
    fallbackText: string;
}) {
    const { isOpen } = useMorphingDialog();
    const [shouldRender, setShouldRender] = useState(false);

    useEffect(() => {
        if (isOpen) {
            const timer = setTimeout(() => setShouldRender(true), 300);
            return () => clearTimeout(timer);
        }
    }, [isOpen]);

    if (!isOpen) {
        // Ensure we reset render state without triggering `setState` inside an effect.
        if (shouldRender) setShouldRender(false);
        return null;
    }

    return <JsonContent content={content} fallbackText={fallbackText} shouldRender={shouldRender} />;
}

/**
 * 单条日志卡片
 */
export function LogCard({ log }: { log: RelayLog }) {
    const t = useTranslations('log.card');
    const { Avatar: ModelAvatar, color: brandColor } = useMemo(
        () => getModelIcon(log.actual_model_name),
        [log.actual_model_name]
    );

    const hasError = !!log.error;
    const isForwarded = log.actual_model_name !== log.request_model_name;

    return (
        <MorphingDialog
            transition={{
                type: 'spring',
                bounce: 0.05,
                duration: 0.25,
            }}
        >
            <MorphingDialogTrigger
                className={cn(
                    "rounded-3xl border bg-card custom-shadow w-full text-left",
                    "hover:shadow-md transition-shadow duration-200",
                    hasError ? "border-destructive/40" : "border-border",
                )}
            >
                <div className="p-4">
                    <div className="grid grid-cols-[auto_1fr] gap-4">
                        <div className="pt-0.5">
                            <ModelAvatar size={40} />
                        </div>

                        <div className="min-w-0 space-y-3">
                            <div className="flex items-center justify-between gap-3">
                                <div className="flex items-center gap-2 min-w-0 text-sm">
                                    <span
                                        className="font-semibold text-card-foreground truncate"
                                        title={log.request_model_name}
                                    >
                                        {log.request_model_name}
                                    </span>
                                    {isForwarded && (
                                        <>
                                            <ArrowRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground/50" />
                                            <Badge
                                                variant="secondary"
                                                className="shrink-0 text-xs px-1.5 py-0"
                                                style={{ backgroundColor: `${brandColor}15`, color: brandColor }}
                                            >
                                                {log.channel_name}
                                            </Badge>
                                            <span
                                                className="text-muted-foreground truncate"
                                                title={log.actual_model_name}
                                            >
                                                {log.actual_model_name}
                                            </span>
                                        </>
                                    )}
                                    {!isForwarded && (
                                        <Badge
                                            variant="secondary"
                                            className="shrink-0 text-xs px-1.5 py-0"
                                            style={{ backgroundColor: `${brandColor}15`, color: brandColor }}
                                        >
                                            {log.channel_name}
                                        </Badge>
                                    )}
                                </div>
                            </div>
                            <div className="grid grid-cols-2 md:grid-cols-6 gap-x-4 gap-y-2">
                                <div className="flex items-center gap-1.5">
                                    <Clock className="h-3.5 w-3.5 shrink-0" style={{ color: brandColor }} />
                                    <span className="text-xs tabular-nums text-muted-foreground truncate">
                                        {formatTime(log.time)}
                                    </span>
                                </div>
                                <div className="flex items-center gap-1.5">
                                    <Zap className="h-3.5 w-3.5 shrink-0 text-amber-500" />
                                    <span className="text-xs tabular-nums text-muted-foreground">
                                        {t('firstToken')} {formatDuration(log.ftut)}
                                    </span>
                                </div>
                                <div className="flex items-center gap-1.5">
                                    <Cpu className="h-3.5 w-3.5 shrink-0 text-blue-500" />
                                    <span className="text-xs tabular-nums text-muted-foreground">
                                        {t('totalTime')} {formatDuration(log.use_time)}
                                    </span>
                                </div>
                                <div className="flex items-center gap-1.5">
                                    <ArrowDownToLine className="h-3.5 w-3.5 shrink-0 text-green-500" />
                                    <span className="text-xs tabular-nums text-muted-foreground">
                                        {t('input')} {log.input_tokens.toLocaleString()}
                                    </span>
                                </div>
                                <div className="flex items-center gap-1.5">
                                    <ArrowUpFromLine className="h-3.5 w-3.5 shrink-0 text-purple-500" />
                                    <span className="text-xs tabular-nums text-muted-foreground">
                                        {t('output')} {log.output_tokens.toLocaleString()}
                                    </span>
                                </div>
                                <div className="flex items-center gap-1.5">
                                    <DollarSign className="h-3.5 w-3.5 shrink-0 text-emerald-500" />
                                    <span className="text-xs tabular-nums font-medium text-emerald-600 dark:text-emerald-400">
                                        {t('cost')} {Number(log.cost).toFixed(6)}
                                    </span>
                                </div>
                            </div>
                        </div>
                    </div>

                    {hasError && (
                        <div className="mt-3 ml-14 p-2.5 rounded-xl bg-destructive/10 border border-destructive/20">
                            <p className="text-xs text-destructive line-clamp-2">{log.error}</p>
                        </div>
                    )}
                </div>
            </MorphingDialogTrigger>

            <MorphingDialogContainer>
                <MorphingDialogContent className="relative w-[calc(100vw-2rem)] md:w-[90vw] max-w-5xl max-h-[calc(100vh-2rem)] rounded-3xl border border-border bg-card p-4 md:p-6 overflow-hidden flex flex-col">
                    <MorphingDialogClose className="text-muted-foreground hover:text-foreground transition-colors" />

                    <MorphingDialogTitle className="flex items-center gap-2 md:gap-3 mb-3 md:mb-4 shrink-0">
                        <ModelAvatar size={28} />
                        <div className="flex items-center gap-2 text-sm">
                            <span className="font-semibold text-card-foreground">
                                {log.request_model_name}
                            </span>
                            {isForwarded && (
                                <>
                                    <ArrowRight className="h-3.5 w-3.5 text-muted-foreground/50" />
                                    <Badge
                                        variant="secondary"
                                        className="text-xs px-1.5 py-0"
                                        style={{ backgroundColor: `${brandColor}15`, color: brandColor }}
                                    >
                                        {log.channel_name}
                                    </Badge>
                                    <span className="text-muted-foreground">
                                        {log.actual_model_name}
                                    </span>
                                </>
                            )}
                            {!isForwarded && (
                                <Badge
                                    variant="secondary"
                                    className="text-xs px-1.5 py-0"
                                    style={{ backgroundColor: `${brandColor}15`, color: brandColor }}
                                >
                                    {log.channel_name}
                                </Badge>
                            )}
                        </div>
                    </MorphingDialogTitle>

                    {hasError && (
                        <div className="mb-3 md:mb-4 p-2.5 md:p-3 rounded-xl bg-destructive/10 border border-destructive/20 shrink-0">
                            <div className="flex items-center gap-2 mb-1">
                                <AlertCircle className="h-4 w-4 text-destructive" />
                                <span className="text-sm font-medium text-destructive">{t('errorInfo')}</span>
                            </div>
                            <p className="text-sm text-destructive">{log.error}</p>
                        </div>
                    )}

                    <MorphingDialogDescription
                        disableLayoutAnimation
                        variants={{
                            initial: { opacity: 0, scale: 0.95, y: 10 },
                            animate: { opacity: 1, scale: 1, y: 0 },
                            exit: { opacity: 0, scale: 0.95, y: 10 },
                        }}
                        className="flex-1 overflow-auto min-h-0"
                    >
                        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                            <div className="flex flex-col rounded-2xl border border-border bg-muted/30 overflow-hidden">
                                <div className="flex items-center gap-2 px-3 md:px-4 py-2.5 md:py-3 border-b border-border bg-muted/50">
                                    <Send className="h-4 w-4 text-green-500" />
                                    <span className="text-sm font-medium text-card-foreground">{t('requestContent')}</span>
                                    <Badge variant="secondary" className="ml-auto text-xs">
                                        {log.input_tokens.toLocaleString()} {t('tokens')}
                                    </Badge>
                                </div>
                                <div className="h-[30vh] md:h-[50vh] overflow-auto">
                                    <DeferredJsonContent content={log.request_content} fallbackText={t('noRequestContent')} />
                                </div>
                            </div>

                            <div className="flex flex-col rounded-2xl border border-border bg-muted/30 overflow-hidden">
                                <div className="flex items-center gap-2 px-3 md:px-4 py-2.5 md:py-3 border-b border-border bg-muted/50">
                                    <MessageSquare className="h-4 w-4 text-purple-500" />
                                    <span className="text-sm font-medium text-card-foreground">{t('responseContent')}</span>
                                    <Badge variant="secondary" className="ml-auto text-xs">
                                        {log.output_tokens.toLocaleString()} {t('tokens')}
                                    </Badge>
                                </div>
                                <div className="h-[30vh] md:h-[50vh] overflow-auto">
                                    <DeferredJsonContent content={log.response_content} fallbackText={t('noResponseContent')} />
                                </div>
                            </div>
                        </div>

                        <div className="flex flex-wrap items-center gap-3 md:gap-4 mt-4 pt-4 border-t border-border text-xs text-muted-foreground">
                            <div className="flex items-center gap-1.5">
                                <Clock className="h-3.5 w-3.5" style={{ color: brandColor }} />
                                <span className="tabular-nums">{formatTime(log.time)}</span>
                            </div>
                            <div className="flex items-center gap-1.5">
                                <Zap className="h-3.5 w-3.5 text-amber-500" />
                                <span>{t('firstTokenTime')}: {formatDuration(log.ftut)}</span>
                            </div>
                            <div className="flex items-center gap-1.5">
                                <Cpu className="h-3.5 w-3.5 text-blue-500" />
                                <span>{t('totalTime')}: {formatDuration(log.use_time)}</span>
                            </div>
                            <div className="flex items-center gap-1.5">
                                <DollarSign className="h-3.5 w-3.5 text-emerald-500" />
                                <span className="font-medium text-emerald-600 dark:text-emerald-400">
                                    {t('cost')}: {Number(log.cost).toFixed(6)}
                                </span>
                            </div>
                        </div>
                    </MorphingDialogDescription>
                </MorphingDialogContent>
            </MorphingDialogContainer>
        </MorphingDialog>
    );
}

