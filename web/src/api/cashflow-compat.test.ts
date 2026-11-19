import { describe, expect, it } from 'vitest';
import { quoteSnapshotOf, rechargeQuoteRequestBody } from './recharge';
import { withdrawalPayoutOf, withdrawalSnapshotOf, type Withdrawal } from './withdrawal';

// 充值/提现统一汇率前端的两条兼容性红线:
// 1) 旧服务端只回 credits/bonus/rate —— 报价请求只多带可选 currency, 快照归一对空字段返回 undefined;
// 2) 旧提现单没有 payout_*/快照字段 —— 归一函数必须判定为 legacy, 列表按 ¥ + actual_cents 旧口径展示。

describe('rechargeQuoteRequestBody', () => {
    it('不带币种时请求体与旧接口完全一致', () => {
        expect(rechargeQuoteRequestBody(1000)).toEqual({ amount_cents: 1000 });
        expect(rechargeQuoteRequestBody(1000, undefined)).toEqual({ amount_cents: 1000 });
        expect(rechargeQuoteRequestBody(1000, '')).toEqual({ amount_cents: 1000 });
    });
    it('选择币种时只追加 currency 字段, 不改 amount_cents 口径', () => {
        expect(rechargeQuoteRequestBody(1000, 'EUR')).toEqual({ amount_cents: 1000, currency: 'EUR' });
    });
});

describe('quoteSnapshotOf', () => {
    it('旧服务端无快照字段时返回 undefined, 界面不显示快照行', () => {
        expect(quoteSnapshotOf(undefined)).toBeUndefined();
        expect(quoteSnapshotOf({ credits: 7 } as never)).toBeUndefined();
    });
    it('新字段名 rate_day/quote_currency 与旧命名 snapshot_day/currency 都能归一', () => {
        expect(quoteSnapshotOf({ rate_day: '20260107', quote_currency: 'EUR', rate_source: 'ecb' }))
            .toEqual({ day: '20260107', source: 'ecb', currency: 'EUR' });
        expect(quoteSnapshotOf({ snapshot_day: '20260107', currency: 'JPY', rate_source: 'manual' }))
            .toEqual({ day: '20260107', source: 'manual', currency: 'JPY' });
    });
});

const legacyRow: Withdrawal = {
    id: 7, user_id: 1, username: 'alice', amount: 100, fee: 1, actual: 99, rate: 7.2,
    actual_cents: 71280, method: 'alipay', account_name: 'a', account_no: 'b', status: 'paid',
    review_note: '', reviewed_by: 0, reviewed_at: null, paid_ref: 'SN', paid_at: null, applied_at: '2026-01-01T00:00:00Z',
};

describe('withdrawal legacy 判定', () => {
    it('无 payout_*/快照字段的历史旧行按 legacy 展示(¥ + actual_cents)', () => {
        expect(withdrawalPayoutOf(legacyRow)).toBeUndefined();
        expect(withdrawalSnapshotOf(legacyRow)).toBeUndefined();
    });
    it('新单按 payout_currency/payout_amount 展示到账币种', () => {
        const row: Withdrawal = { ...legacyRow, payout_currency: 'eur', payout_amount: 12.34 };
        expect(withdrawalPayoutOf(row)).toEqual({ currency: 'eur', amount: 12.34 });
    });
    it('快照归一兼容 rate_day/quote_currency 与 snapshot_day/currency 两条命名', () => {
        const newRow: Withdrawal = { ...legacyRow, rate_day: '20260107', quote_currency: 'HKD', rate_source: 'ecb' };
        expect(withdrawalSnapshotOf(newRow)).toEqual({ day: '20260107', source: 'ecb', currency: 'HKD' });
        const aliased: Withdrawal = { ...legacyRow, snapshot_day: '20260107', currency: 'HKD' };
        expect(withdrawalSnapshotOf(aliased)).toEqual({ day: '20260107', source: '', currency: 'HKD' });
    });
});
