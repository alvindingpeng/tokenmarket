import { useTranslations } from 'use-intl';
import { Button } from '@/components/ui/button';
import { unsavedKeys, type BatchSaveOutcome } from './paymentSave';

// PaymentUnsavedNotice 批量保存部分失败时常驻的错误条。
//
// 为什么它必须常驻而不是只弹 toast: 支付配置是一张长表单, 用户改完点保存时可能滚在页面另一头,
// 等滚回来 toast 早就消失了, 而那几个输入框仍是「看着像已保存」的样子。
// 标记要一直留到下次保存成功。
//
// 拆成独立组件是为了让它可被单测直接渲染: 它只依赖一个 outcome 对象, 不需要真的点一次保存。
export function PaymentUnsavedNotice({ outcome, onDismiss }: {
    outcome: BatchSaveOutcome;
    onDismiss: () => void;
}) {
    const t = useTranslations('setting');
    const missing = unsavedKeys(outcome);
    return (
        <div role='alert' className='flex flex-wrap items-start gap-2 rounded-2xl border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive'>
            <span className='min-w-0 flex-1'>
                {t('payment.partialFailed', {
                    saved: outcome.saved.length,
                    total: outcome.saved.length + missing.length,
                    keys: missing.join(', '),
                })}
            </span>
            <Button type='button' variant='outline' size='sm' className='h-7 rounded-lg px-2 text-xs' onClick={onDismiss}>
                {t('payment.unsavedDismiss')}
            </Button>
        </div>
    );
}
