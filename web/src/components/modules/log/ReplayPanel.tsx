import { useEffect, useState } from 'react';
import { Loader2, RotateCcw, Send } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { type ReplayResult, useReplayRequest } from '@/api/log';
import { Button } from '@/components/ui/button';
import { Textarea } from '@/components/ui/textarea';
import { Badge } from '@/components/ui/badge';
import { CopyIconButton } from '@/components/common/CopyButton';

// ReplayPanel 请求重放控制台: 编辑原始请求体后经原 API Key 重放, 结果就地展示。
// 重放走与客户端调用完全相同的转发链路(认证/限流/计费/路由), 会产生一条新的调用日志。
export function ReplayPanel({ requestId, initialBody, supported }: {
    requestId: number;
    initialBody: string;
    supported: boolean;
}) {
    const t = useTranslations('log.card');
    const [body, setBody] = useState(initialBody);
    const [dirty, setDirty] = useState(false); // dirty 后不再跟随请求体查询的异步到达, 避免覆盖手改内容。
    const [result, setResult] = useState<ReplayResult | null>(null);
    const replay = useReplayRequest();

    // 请求体是按需查询的, 打开面板时可能晚于首次渲染到达; 未手改前保持同步。
    useEffect(() => {
        if (!dirty) setBody(initialBody);
    }, [initialBody, dirty]);

    if (!supported) {
        return (
            <div className="flex h-full items-center justify-center px-6 text-xs text-muted-foreground">
                {t('replayUnsupported')}
            </div>
        );
    }

    return (
        <div className="flex h-full min-h-0 flex-col gap-2 p-3">
            <div className="flex items-center gap-2">
                <span className="text-xs font-medium text-card-foreground">{t('replayRequestBody')}</span>
                <Button
                    variant="ghost"
                    size="sm"
                    className="h-6 px-2 text-xs"
                    disabled={replay.isPending}
                    onClick={() => {
                        setDirty(false);
                        setBody(initialBody);
                    }}
                >
                    <RotateCcw aria-hidden="true" className="size-3" />
                    {t('replayReset')}
                </Button>
                <Button
                    size="sm"
                    className="ml-auto h-6 px-2 text-xs"
                    disabled={replay.isPending || !body.trim()}
                    onClick={async () => {
                        try {
                            const data = await replay.mutateAsync({ requestId, requestBody: body });
                            setResult(data);
                        } catch (cause) {
                            toast.error(t('replayFailed'), { description: cause instanceof Error ? cause.message : undefined });
                        }
                    }}
                >
                    {replay.isPending ? <Loader2 aria-hidden="true" className="size-3 animate-spin" /> : <Send aria-hidden="true" className="size-3" />}
                    {replay.isPending ? t('replaySending') : t('replaySend')}
                </Button>
            </div>
            <Textarea
                value={body}
                onChange={(event) => {
                    setDirty(true);
                    setBody(event.target.value);
                }}
                spellCheck={false}
                className="h-44 resize-none font-mono text-[11px] leading-relaxed"
            />
            {result && (
                <div className="flex min-h-0 flex-1 flex-col gap-2">
                    <div className="flex items-center gap-2">
                        <Badge variant={result.status_code >= 400 ? 'destructive' : 'secondary'} className="text-xs tabular-nums">
                            HTTP {result.status_code}
                        </Badge>
                        <span className="text-xs tabular-nums text-muted-foreground">
                            {t('replayResultDuration', { ms: result.duration_ms })}
                        </span>
                        {result.response_body !== '' && (
                            <CopyIconButton
                                text={result.response_body}
                                className="ml-auto rounded-md p-1 text-muted-foreground/60 transition-colors hover:bg-muted hover:text-foreground"
                                copyIconClassName="size-3.5"
                                checkIconClassName="size-3.5"
                            />
                        )}
                    </div>
                    <pre className="min-h-0 flex-1 overflow-auto whitespace-pre-wrap wrap-break-word rounded-md border border-border bg-muted/30 p-2 text-[11px] leading-relaxed">
                        {result.response_body || t('replayResultEmpty')}
                    </pre>
                </div>
            )}
            <p className="text-[10px] leading-relaxed text-muted-foreground">{t('replayHint')}</p>
        </div>
    );
}
