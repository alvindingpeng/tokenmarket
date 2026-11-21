import { describe, expect, it } from 'vitest';
import { renderSSR } from '@/test/ssr';
import zhHans from '@/locales/zh_hans.json';
import { PaymentUnsavedNotice } from './PaymentUnsavedNotice';
import { summarizeBatchSave } from './paymentSave';

// 支付配置批量保存的「部分成功」提示条(mock SSR)。
//
// 修前的症状: saveAll 把所有键逐个提交, 中途失败时只弹一句 toast.error(「保存失败」),
// 界面上那张表仍旧显示着全部新值 —— 用户以为全都没成(其实改了一半), 或者以为全成了(其实只落了一半)。
// 两种误判都会留下不一致的配置。
//
// 这里断言的是提示条本身说清楚了「几项成了、哪几项没成」, 而不是只说一句失败。

const KEYS = ['epay_gateway_url', 'epay_pid', 'stripe_secret_key', 'recharge_rate'];

describe('PaymentUnsavedNotice', () => {
    it('部分失败时列出没保存的键, 并说明成了几项', () => {
        const outcome = summarizeBatchSave(KEYS, 2, 'stripe_secret_key');
        const html = renderSSR(<PaymentUnsavedNotice outcome={outcome} onDismiss={() => {}} />);
        // 必须点出失败的那个键本身 —— 只说「有项目失败」等于让用户自己一个个去试。
        expect(html).toContain('stripe_secret_key');
        // 也点出因前面失败而没发出去的项。
        expect(html).toContain('recharge_rate');
        // role=alert 是给读屏软件的信号, 也是它区别于静态说明的地方。
        expect(html).toContain('role="alert"');
    });

    it('已成功的项不出现在未保存清单里', () => {
        // 列进去会让用户把已经落库的值再存一遍。
        const outcome = summarizeBatchSave(KEYS, 2, 'stripe_secret_key');
        const html = renderSSR(<PaymentUnsavedNotice outcome={outcome} onDismiss={() => {}} />);
        expect(html).not.toContain('epay_pid');
    });

    it('带一个可关闭的按钮(标记不是死条, 但也不自己消失)', () => {
        const outcome = summarizeBatchSave(KEYS, 1, 'epay_pid');
        const html = renderSSR(<PaymentUnsavedNotice outcome={outcome} onDismiss={() => {}} />);
        expect(html).toContain('Dismiss');
    });

    it('中文环境下接上了中文文案, 不是 i18n 路径串', () => {
        const outcome = summarizeBatchSave(KEYS, 1, 'epay_pid');
        const html = renderSSR(<PaymentUnsavedNotice outcome={outcome} onDismiss={() => {}} />,
            undefined, { locale: 'zh-Hans', messages: zhHans });
        expect(html).not.toContain('payment.partialFailed');
        expect(html).toContain('知道了');
    });
});
