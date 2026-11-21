import { useTranslations } from 'use-intl';
import { Button } from '@/components/ui/button';
import { budgetRowMode, type BudgetRowMode } from './opsDataState';

// BudgetRowActions 预算表格一行的操作区: 空闲 / 等确认停用启用 / 等确认删除。
//
// 为什么拆成独立组件: 破坏性操作要两次点击, 而「第一次点击只切状态、不发请求」这件事
// 只有把三种模式渲染出来才看得见。留在 columns 的闭包里的话, 单测碰不到它。
export function BudgetRowActions({
    rowId,
    enabled,
    pending,
    deleting,
    toggling,
    onAsk,
    onCancel,
    onDelete,
    onToggle,
}: {
    rowId: number;
    enabled: boolean;
    pending: { id: number; kind: 'delete' | 'toggle' } | null;
    deleting: boolean;
    toggling: boolean;
    onAsk: (next: { id: number; kind: 'delete' | 'toggle' }) => void;
    onCancel: () => void;
    onDelete: () => void;
    onToggle: () => void;
}) {
    const t = useTranslations('ops');
    const mode: BudgetRowMode = budgetRowMode(pending, rowId);

    if (mode === 'confirmDelete') {
        return (
            <span className='flex flex-wrap items-center gap-1'>
                {/* 确认文案要说清这一下真的会删, 而不是只说再点一次。 */}
                <span className='text-xs text-muted-foreground'>{t('budgets.deleteConfirm')}</span>
                <Button size='sm' variant='destructive' className='h-7 px-2 text-xs' disabled={deleting} onClick={onDelete}>
                    {t('budgets.confirmDelete')}
                </Button>
                <Button size='sm' variant='outline' className='h-7 px-2 text-xs' onClick={onCancel}>{t('budgets.confirmCancel')}</Button>
            </span>
        );
    }

    if (mode === 'confirmToggle') {
        return (
            <span className='flex flex-wrap items-center gap-1'>
                {/* 停用一条 block 级预算等于当天放行本该被拦下的请求, 所以它和删除一样要确认。 */}
                <span className='text-xs text-muted-foreground'>{enabled ? t('budgets.disableConfirm') : t('budgets.enableConfirm')}</span>
                <Button size='sm' variant='destructive' className='h-7 px-2 text-xs' disabled={toggling} onClick={onToggle}>
                    {enabled ? t('budgets.confirmDisable') : t('budgets.confirmEnable')}
                </Button>
                <Button size='sm' variant='outline' className='h-7 px-2 text-xs' onClick={onCancel}>{t('budgets.confirmCancel')}</Button>
            </span>
        );
    }

    // 空闲态: 两个按钮只切状态, 不发任何请求 —— 这就是连点两下才删的全部实现。
    return (
        <span className='flex gap-1'>
            <Button size='sm' variant='ghost' className='h-7 px-2 text-xs' onClick={() => onAsk({ id: rowId, kind: 'toggle' })}>
                {enabled ? t('budgets.disable') : t('budgets.enable')}
            </Button>
            <Button size='sm' variant='ghost' className='h-7 px-2 text-xs text-red-600 dark:text-red-400' onClick={() => onAsk({ id: rowId, kind: 'delete' })}>
                {t('budgets.delete')}
            </Button>
        </span>
    );
}
