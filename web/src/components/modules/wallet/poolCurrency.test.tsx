import { describe, expect, it } from 'vitest';
import { renderSSR } from '@/test/ssr';
import { MorphingDialog } from '@/components/ui/morphing-dialog';
import Wallet, { WithdrawDialogContent } from './index';

// 提现与钱包口径的金额币种标注。
//
// 修前的症状(wallet/index.tsx, 见本文件末尾的失败原文):
//   - 提现弹窗「可提余额 / 手续费 / 到手额」三处都是 formatAmount(...) 裸数字;
//   - 钱包页五格摘要卡片同样裸数字;
//   - 用户只能从旁边那句「1 USD ≈ 7」猜单位, 而那句说的是**打款币种**折算,
//     与这些钱本身的记账币种(收益池 USD)不是一回事。结果: 把 USD 读成 CNY。
//
// 这一组断言钉的是用户真正看得见的那串文字(金额 + 币种代码一起), 不是组件 props。

const withdrawKey = ['withdraw', 'summary'] as const;
const fxKey = ['fx-rates'] as const;
const walletKey = ['wallet', 'summary'] as const;
const ledgerKey = ['wallet', 'ledger', '', 50, 0, 0] as const;

const FX_ITEMS = [{ from: 'USD', to: 'CNY', rate: 7, day: '20260107', source: 'ecb' }];

// summaryWith 造一份提现口径; poolCurrency 传 undefined 表示「后端还没下发这个字段」。
function summaryWith(poolCurrency: string | undefined) {
    return {
        available: 100,
        frozen: 20,
        withdrawn: 5,
        revenue: 125,
        rate: 7,
        currency: 'CNY',
        quote_currency: 'CNY',
        fee_rate: 0.01,
        fee_min: 1,
        min_amount: 50,
        can_apply: true,
        supported_currencies: ['CNY', 'EUR'],
        ...(poolCurrency === undefined ? {} : { pool_currency: poolCurrency }),
    };
}

// renderWithdrawDialog 渲染提现弹窗正文。
// 正文在真实页面里挂在 MorphingDialogContainer 的 portal 上, 页面级 mock SSR 拿不到
// portal 内容, 所以直接渲染正文并套上它所需的 provider。
// initialAmount 是给 SSR 用的初值(弹窗金额是受控输入, 服务端渲染没法触发 on_change)。
function renderWithdrawDialog(poolCurrency: string | undefined, initialAmount = '100') {
    return renderSSR(
        <MorphingDialog>
            <WithdrawDialogContent onApplied={() => {}} initialAmount={initialAmount} />
        </MorphingDialog>,
        (client) => {
            client.setQueryData(withdrawKey, summaryWith(poolCurrency));
            client.setQueryData(fxKey, { day: '20260107', items: FX_ITEMS });
        },
    );
}

describe('提现弹窗: 池币种标注', () => {
    it('pool_currency=USD 时可提额/手续费/到手额三处都带 USD', () => {
        const html = renderWithdrawDialog('USD');
        // 可提余额 100 / 手续费 max(1, 100x1%)=1 / 到手 99 —— 三个数字互不相同,
        // 任何一处漏标或标错都能被单独抓到。
        expect(html).toContain('>100 USD<');
        expect(html).toContain('>1 USD<');
        expect(html).toContain('99 USD');
    });

    it('规则金额(最低费/最低提现额)同样带池币种', () => {
        const html = renderWithdrawDialog('USD');
        // fee_min=1 / min_amount=50 都是收益池口径的规则值, 不标单位就会被当成 1 元、50 元人民币。
        // 这两处只改占位符实参、不新增文案键, 所以三语不用动。
        expect(html).toContain('Fee = max(min 1 USD, amount x 1%)');
        expect(html).toContain('Minimum withdrawal is 50 USD.');
        // 缺币种时这两处也要退回纯数字, 不留孤立的 USD。
        const withoutPool = renderWithdrawDialog(undefined);
        expect(withoutPool).toContain('Fee = max(min 1, amount x 1%)');
        expect(withoutPool).toContain('Minimum withdrawal is 50.');
    });

    it('池币种与打款币种各标各的: ≈ 折算仍是打款币种 CNY', () => {
        const html = renderWithdrawDialog('USD');
        // 到手 99 USD 与 ≈693 CNY(99x7) 必须同时出现且各带自己的代码,
        // 用户才分得清「我要提多少」与「到账多少」。修前两处都没有单位。
        expect(html).toContain('99 USD');
        expect(html).toContain('693 CNY');
        // 池币种只标在提现口径的金额上, 不能被误写成 CNY。
        expect(html).not.toContain('100 CNY');
        expect(html).not.toContain('1 CNY<');
    });

    it('pool_currency 缺失时不标币种, 金额照常显示', () => {
        const html = renderWithdrawDialog(undefined);
        // 不猜 CNY(把美元当人民币比不显示更糟), 也不猜 USD。
        expect(html).not.toMatch(/100 (USD|CNY)/);
        expect(html).not.toMatch(/99 (USD|CNY)/);
        // 金额本身必须还在 —— 取不到币种不等于取不到金额, 更不能退化成 0/空。
        expect(html).toContain('>100<');
        expect(html).toContain('99');
        // 打款币种折算不受影响(它与池币种是两个来源)。
        expect(html).toContain('693 CNY');
    });

    it('选到打款币种 EUR 时只动 ≈ 那一处, 池币种标注不变', () => {
        // 回归: 打款币种下拉改的是 payoutCurrency 那一列, 与 pool_currency 无关。
        // EUR 牌价 = 0.9: 到手 99 USD 折算 = 89.1 EUR。
        const html = renderSSR(
            <MorphingDialog>
                <WithdrawDialogContent onApplied={() => {}} initialAmount="100" initialPayoutCurrency="EUR" />
            </MorphingDialog>,
            (client) => {
                client.setQueryData(withdrawKey, summaryWith('USD'));
                client.setQueryData(fxKey, {
                    day: '20260107',
                    items: [...FX_ITEMS, { from: 'USD', to: 'EUR', rate: 0.9, day: '20260107', source: 'ecb' }],
                });
            },
        );
        expect(html).toContain('89.1 EUR');
        // 池币种那三处仍然是 USD, 没有被打款币种污染。
        expect(html).toContain('>100 USD<');
        expect(html).toContain('99 USD');
        expect(html).not.toContain('100 EUR');
    });
});

describe('钱包页摘要卡片: 池币种标注', () => {
    // WALLET_SUMMARY 五格取互不相同的正数: 任何一处接错都会被断言抓到。
    const WALLET_SUMMARY = {
        balance: 11.11,
        frozen: 22.22,
        total_recharged: 33.33,
        total_spent: 44.44,
        total_revenue: 55.55,
    };

    function renderWallet(poolCurrency: string | undefined) {
        return renderSSR(<Wallet />, (client) => {
            client.setQueryData(walletKey, poolCurrency === undefined ? WALLET_SUMMARY : { ...WALLET_SUMMARY, pool_currency: poolCurrency });
            client.setQueryData(ledgerKey, { items: [], total: 0 });
        });
    }

    it('pool_currency=USD 时五格金额都带 USD', () => {
        const html = renderWallet('USD');
        for (const value of ['11.11', '22.22', '33.33', '44.44', '55.55']) {
            expect(html).toContain(value + ' USD');
        }
    });

    it('pool_currency 缺失时五格不标币种, 也不把金额弄成 0/占位符', () => {
        const html = renderWallet(undefined);
        // 五格金额的 title 是完整文字, 用它做精确断言: 单位缺席时只能是纯数字。
        for (const value of ['11.11', '22.22', '33.33', '44.44', '55.55']) {
            expect(html).toContain('title="' + value + '"');
            expect(html).not.toContain(value + ' USD');
        }
        // 不许出现「未下发就猜成 CNY」。
        expect(html).not.toContain('11.11 CNY');
        // 也不许跌回「—」占位符或 0: 这些是真实数值。
        expect(html.split('title="—"').length - 1).toBe(0);
        expect(html).not.toContain('title="0.00"');
    });
});
