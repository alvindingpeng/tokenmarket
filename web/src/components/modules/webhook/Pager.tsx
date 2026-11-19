import { useTranslations } from 'use-intl';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import { Button } from '@/components/ui/button';

// Pager 通用分页控件: 上一页/下一页 + 页码与总数(与 billing/工单同款交互)。
// 订阅列表与投递日志各有一份分页, scope 决定文案键(webhook.pagination / webhook.logs.pagination)。
// 翻页按钮是纯图标, 补 aria-label 供读屏识别。
export function Pager({ page, total, pageSize, onChange, scope }: {
    page: number;
    total: number;
    pageSize: number;
    onChange: (page: number) => void;
    scope: 'pagination' | 'logs.pagination';
}) {
    const t = useTranslations('webhook');
    const pages = Math.max(1, Math.ceil(total / pageSize));

    return (
        <nav aria-label={t(scope + '.label')} className="flex shrink-0 items-center justify-between gap-2 px-1 text-xs text-muted-foreground">
            <span className="tabular-nums">{t(scope + '.summary', { current: page, pages, count: total })}</span>
            <div className="flex items-center gap-1.5">
                <Button
                    variant="outline"
                    size="sm"
                    className="size-7 rounded-lg p-0"
                    disabled={page <= 1}
                    onClick={() => onChange(page - 1)}
                    aria-label={t(scope + '.prev')}
                >
                    <ChevronLeft className="size-4" aria-hidden="true" />
                </Button>
                <Button
                    variant="outline"
                    size="sm"
                    className="size-7 rounded-lg p-0"
                    disabled={page >= pages}
                    onClick={() => onChange(page + 1)}
                    aria-label={t(scope + '.next')}
                >
                    <ChevronRight className="size-4" aria-hidden="true" />
                </Button>
            </div>
        </nav>
    );
}
