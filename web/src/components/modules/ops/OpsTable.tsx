import type { ReactNode } from 'react';
import { CircleAlert, RefreshCw } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { Button } from '@/components/ui/button';

// OpsTable 运维页通用表格: 空态、横向滚动、行悬停统一处理。
export type Col<T> = { key: string; label: string; render?: (row: T) => ReactNode };
export function OpsTable<T>({ columns, rows, empty, rowKey }: {
    columns: Col<T>[];
    rows: T[];
    empty: string;
    rowKey: (row: T) => string;
}) {
    if (rows.length === 0) {
        return <div className='rounded-3xl border border-border bg-card p-10 text-center text-sm text-muted-foreground'>{empty}</div>;
    }
    return (
        <div className='overflow-x-auto rounded-3xl border border-border bg-card'>
            <table className='w-full min-w-[760px] text-sm'>
                <thead>
                    <tr className='border-b border-border text-left text-xs text-muted-foreground'>
                        {columns.map((column) => (
                            <th key={column.key} className='whitespace-nowrap px-4 py-3 font-medium'>{column.label}</th>
                        ))}
                    </tr>
                </thead>
                <tbody>
                    {rows.map((row) => (
                        <tr key={rowKey(row)} className='border-b border-border/60 last:border-0 hover:bg-muted/40'>
                            {columns.map((column) => (
                                <td key={column.key} className='px-4 py-2.5 align-middle'>
                                    {column.render ? column.render(row) : String((row as Record<string, unknown>)[column.key] ?? '-')}
                                </td>
                            ))}
                        </tr>
                    ))}
                </tbody>
            </table>
        </div>
    );
}

// LoadError 取数失败的统一占位: 说明 + 重试。
//
// 为什么不能复用空的表格: 两者的界面必须长得不一样。此前预算表格只解构 data, 不看 isError ——
// 请求失败时 data 是 undefined, 列表渲染成空, 屏幕上显示的是「还没有建过预算」。
// 管理员据此以为预算被人删光了, 而真实原因是这一次接口调用失败。空是数据, 失败不是。
export function LoadError({ message, onRetry, retrying }: {
    message: string;
    onRetry?: () => void;
    retrying?: boolean;
}) {
    const t = useTranslations('common');
    return (
        <div role='alert' className='flex flex-col items-center gap-3 rounded-3xl border border-border bg-card p-10 text-center'>
            <CircleAlert className='size-6 text-muted-foreground' aria-hidden='true' />
            <p className='text-sm text-muted-foreground'>{message}</p>
            {onRetry && (
                <Button variant='outline' size='sm' className='rounded-xl' onClick={onRetry} disabled={retrying}>
                    <RefreshCw className={'size-4' + (retrying ? ' animate-spin' : '')} aria-hidden='true' />
                    {t('retry')}
                </Button>
            )}
        </div>
    );
}

// StaleWarning 有旧数据但这次刷新失败时的提示条。
//
// 保留旧数据是有意的(比空白有用), 但**必须说明它可能过期** —— 静默窗口的「是否生效」直接决定
// 告警会不会外发, 拿一份过期的清单去做判断, 等于照着昨天的地图走今天的路。
export function StaleWarning({ message, onRetry, retrying }: {
    message: string;
    onRetry?: () => void;
    retrying?: boolean;
}) {
    const t = useTranslations('common');
    return (
        <div role='status' className='flex flex-wrap items-center gap-2 rounded-2xl border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs text-amber-800 dark:text-amber-300'>
            <CircleAlert className='size-4 shrink-0' aria-hidden='true' />
            <span className='min-w-0 flex-1'>{message}</span>
            {onRetry && (
                <Button variant='outline' size='sm' className='h-7 rounded-lg px-2 text-xs' onClick={onRetry} disabled={retrying}>
                    {t('retry')}
                </Button>
            )}
        </div>
    );
}
