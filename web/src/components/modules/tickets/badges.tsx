import { useTranslations } from 'use-intl';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import { TICKET_CATEGORIES, type TicketStatus } from '@/api/ticket';

// 状态/优先级/分类徽标: 颜色只是辅助, 文案才是主语义(色弱用户不靠颜色区分状态)。
// 色板沿用 users 模块的既有 token 口径(bg-*-500/10 + dark 文本), 不引入新色。

// StatusBadge 工单状态: 新建蓝 / 处理中黄 / 已解决绿 / 已关闭灰(与路线图状态标签一致)。
export function StatusBadge({ status }: { status: TicketStatus }) {
    const t = useTranslations('tickets');
    const className = status === 'open'
        ? 'bg-blue-500/10 text-blue-600 dark:text-blue-400'
        : status === 'in_progress'
            ? 'bg-amber-500/10 text-amber-600 dark:text-amber-400'
            : status === 'resolved'
                ? 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'
                : 'bg-muted text-muted-foreground';

    return (
        <Badge variant="outline" className={className}>
            {t('status.' + status)}
        </Badge>
    );
}

// PriorityBadge 工单优先级: 紧急红 / 高橙 / 普通默认 / 低灰。
export function PriorityBadge({ priority }: { priority: string }) {
    const t = useTranslations('tickets');
    const className = priority === 'urgent'
        ? 'bg-red-500/10 text-red-600 dark:text-red-400'
        : priority === 'high'
            ? 'bg-orange-500/10 text-orange-600 dark:text-orange-400'
            : priority === 'normal'
                ? ''
                : 'bg-muted text-muted-foreground';

    return (
        <Badge variant={priority === 'normal' ? 'secondary' : 'outline'} className={className}>
            {t('priority.' + priority)}
        </Badge>
    );
}

// CategoryBadge 工单分类; 后端 category 是自由串, 不在枚举里的原样展示, 不猜文案(缺键会抛错)。
export function CategoryBadge({ category }: { category: string }) {
    const t = useTranslations('tickets');
    if (!category) return <span className="text-muted-foreground">{'\u2014'}</span>;
    return (
        <Badge variant="outline" className={cn('text-muted-foreground')}>
            {TICKET_CATEGORIES.includes(category as never) ? t('category.' + category) : category}
        </Badge>
    );
}
