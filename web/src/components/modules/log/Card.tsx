'use client';

import { useMemo } from 'react';
import { Clock, Cpu, Zap, AlertCircle, ArrowDownToLine, ArrowUpFromLine, DollarSign, ArrowRight, Send, MessageSquare } from 'lucide-react';
import { useTranslations } from 'next-intl';
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
                    {/* 主布局: Grid 两列 - 头像固定左上角 */}
                    <div className="grid grid-cols-[auto_1fr] gap-4">
                        {/* 左侧: 头像 - 顶部对齐 */}
                        <div className="pt-0.5">
                            <ModelAvatar size={40} />
                        </div>

                        {/* 右侧: 全部内容 */}
                        <div className="min-w-0 space-y-3">
                            {/* 第一行: 模型转发信息 + 状态 */}
                            <div className="flex items-center justify-between gap-3">
                                {/* 模型转发路径: request → channel actual */}
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
                                {/* 错误状态 */}
                                {hasError && (
                                    <Badge variant="destructive" className="shrink-0 gap-1">
                                        <AlertCircle className="h-3 w-3" />
                                        {t('error')}
                                    </Badge>
                                )}
                            </div>

                            {/* 统计数据 Grid: 移动端 2列3行, 桌面端 6列1行 */}
                            <div className="grid grid-cols-2 md:grid-cols-6 gap-x-4 gap-y-2">
                                {/* 时间 */}
                                <div className="flex items-center gap-1.5">
                                    <Clock className="h-3.5 w-3.5 shrink-0" style={{ color: brandColor }} />
                                    <span className="text-xs tabular-nums text-muted-foreground truncate">
                                        {formatTime(log.time)}
                                    </span>
                                </div>
                                {/* 首字时间 */}
                                <div className="flex items-center gap-1.5">
                                    <Zap className="h-3.5 w-3.5 shrink-0 text-amber-500" />
                                    <span className="text-xs tabular-nums text-muted-foreground">
                                        {t('firstToken')} {formatDuration(log.ftut)}
                                    </span>
                                </div>
                                {/* 总耗时 */}
                                <div className="flex items-center gap-1.5">
                                    <Cpu className="h-3.5 w-3.5 shrink-0 text-blue-500" />
                                    <span className="text-xs tabular-nums text-muted-foreground">
                                        {t('totalTime')} {formatDuration(log.use_time)}
                                    </span>
                                </div>
                                {/* 输入 tokens */}
                                <div className="flex items-center gap-1.5">
                                    <ArrowDownToLine className="h-3.5 w-3.5 shrink-0 text-green-500" />
                                    <span className="text-xs tabular-nums text-muted-foreground">
                                        {t('input')} {log.input_tokens.toLocaleString()}
                                    </span>
                                </div>
                                {/* 输出 tokens */}
                                <div className="flex items-center gap-1.5">
                                    <ArrowUpFromLine className="h-3.5 w-3.5 shrink-0 text-purple-500" />
                                    <span className="text-xs tabular-nums text-muted-foreground">
                                        {t('output')} {log.output_tokens.toLocaleString()}
                                    </span>
                                </div>
                                {/* 费用 */}
                                <div className="flex items-center gap-1.5">
                                    <DollarSign className="h-3.5 w-3.5 shrink-0 text-emerald-500" />
                                    <span className="text-xs tabular-nums font-medium text-emerald-600 dark:text-emerald-400">
                                        {t('cost')} {Number(log.cost).toFixed(6)}
                                    </span>
                                </div>
                            </div>
                        </div>
                    </div>

                    {/* 错误信息 */}
                    {hasError && (
                        <div className="mt-3 ml-14 p-2.5 rounded-xl bg-destructive/10 border border-destructive/20">
                            <p className="text-xs text-destructive line-clamp-2">{log.error}</p>
                        </div>
                    )}
                </div>
            </MorphingDialogTrigger>

            <MorphingDialogContainer>
                <MorphingDialogContent className="relative w-[90vw] max-w-5xl rounded-3xl border border-border bg-card p-6">
                    <MorphingDialogClose className="text-muted-foreground hover:text-foreground transition-colors" />

                    {/* 头部信息 */}
                    <MorphingDialogTitle className="flex items-center gap-3 mb-4">
                        <ModelAvatar size={32} />
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

                    {/* 错误信息 */}
                    {hasError && (
                        <div className="mb-4 p-3 rounded-xl bg-destructive/10 border border-destructive/20">
                            <div className="flex items-center gap-2 mb-1">
                                <AlertCircle className="h-4 w-4 text-destructive" />
                                <span className="text-sm font-medium text-destructive">{t('errorInfo')}</span>
                            </div>
                            <p className="text-sm text-destructive">{log.error}</p>
                        </div>
                    )}

                    {/* 双列内容区 */}
                    <MorphingDialogDescription
                        disableLayoutAnimation
                        variants={{
                            initial: { opacity: 0, scale: 0.95, y: 10 },
                            animate: { opacity: 1, scale: 1, y: 0 },
                            exit: { opacity: 0, scale: 0.95, y: 10 },
                        }}
                    >
                        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                            {/* 左侧: 请求内容 */}
                            <div className="flex flex-col rounded-2xl border border-border bg-muted/30 overflow-hidden">
                                <div className="flex items-center gap-2 px-4 py-3 border-b border-border bg-muted/50">
                                    <Send className="h-4 w-4 text-green-500" />
                                    <span className="text-sm font-medium text-card-foreground">{t('requestContent')}</span>
                                    <Badge variant="secondary" className="ml-auto text-xs">
                                        {log.input_tokens.toLocaleString()} {t('tokens')}
                                    </Badge>
                                </div>
                                <div className="h-[50vh] overflow-auto">
                                    <pre className="p-4 text-xs text-muted-foreground whitespace-pre-wrap break-words font-mono leading-relaxed">
                                        {log.request_content || t('noRequestContent')}
                                    </pre>
                                </div>
                            </div>

                            {/* 右侧: 响应内容 */}
                            <div className="flex flex-col rounded-2xl border border-border bg-muted/30 overflow-hidden">
                                <div className="flex items-center gap-2 px-4 py-3 border-b border-border bg-muted/50">
                                    <MessageSquare className="h-4 w-4 text-purple-500" />
                                    <span className="text-sm font-medium text-card-foreground">{t('responseContent')}</span>
                                    <Badge variant="secondary" className="ml-auto text-xs">
                                        {log.output_tokens.toLocaleString()} {t('tokens')}
                                    </Badge>
                                </div>
                                <div className="h-[50vh] overflow-auto">
                                    <pre className="p-4 text-xs text-muted-foreground whitespace-pre-wrap break-words font-mono leading-relaxed">
                                        {log.response_content || t('noResponseContent')}
                                    </pre>
                                </div>
                            </div>
                        </div>

                        {/* 底部统计信息 */}
                        <div className="flex flex-wrap items-center gap-4 mt-4 pt-4 border-t border-border text-xs text-muted-foreground">
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

