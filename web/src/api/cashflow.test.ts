import { describe, expect, it } from 'vitest';
import { quoteSnapshotOf, rechargeQuoteRequestBody } from './recharge';
import { withdrawalPayoutOf, withdrawalSnapshotOf, type Withdrawal } from './withdrawal';

const baseWithdrawal: Withdrawal = {
    id: 1,
    user_id: 7,
    username: 'r',
    amount: 100,
    fee: 1,
    actual: 99,
    rate: 7.2,
    actual_cents: 71280,
    method: 'alipay',
    account_name: 'a',
    account_no: 'b',
    status: 'paid',
    review_note: '',
    reviewed_by: 0,
    reviewed_at: null,
    paid_ref: '',
    paid_at: null,
    applied_at: '2026-01-07T00:00:00Z',
};

describe('quoteSnapshotOf', () => {
    it('新字段 quote_currency/rate_day 优先', () => {
        expect(quoteSnapshotOf({ quote_currency: 'CNY', rate_day: '20260107', rate_source: 'ecb' })).toEqual({
            day: '20260107', source: 'ecb', currency: 'CNY',
        });
    });
    it('旧字段 snapshot_day/currency 兼容并存', () => {
        expect(quoteSnapshotOf({ snapshot_day: '20260107', currency: 'JPY', rate_source: 'manual' })).toEqual({
            day: '20260107', source: 'manual', currency: 'JPY',
        });
    });
    it('无任何快照元数据返回 undefined(旧行 legacy)', () => {
        expect(quoteSnapshotOf(undefined)).toBeUndefined();
        expect(quoteSnapshotOf({})).toBeUndefined();
    });
});

describe('rechargeQuoteRequestBody', () => {
    it('旧接口只带 amount_cents; 币种作为可选扩展', () => {
        expect(rechargeQuoteRequestBody(1000)).toEqual({ amount_cents: 1000 });
        expect(rechargeQuoteRequestBody(1000, 'JPY')).toEqual({ amount_cents: 1000, currency: 'JPY' });
    });
});

describe('withdrawalSnapshotOf', () => {
    it('新行返回快照元数据', () => {
        expect(withdrawalSnapshotOf({ ...baseWithdrawal, quote_currency: 'CNY', rate_day: '20260107', rate_source: 'ecb' })).toEqual({
            day: '20260107', source: 'ecb', currency: 'CNY',
        });
    });
    it('旧行无字段返回 undefined → 展示 legacy 提示', () => {
        expect(withdrawalSnapshotOf(baseWithdrawal)).toBeUndefined();
    });
});

describe('withdrawalPayoutOf', () => {
    it('payout_currency 存在时按到账币种展示', () => {
        expect(withdrawalPayoutOf({ ...baseWithdrawal, payout_currency: 'jpy', payout_amount: 15000 })).toEqual({
            currency: 'jpy', amount: 15000,
        });
    });
    it('无 payout_currency 回退 undefined → 旧行按 ¥actual_cents', () => {
        expect(withdrawalPayoutOf(baseWithdrawal)).toBeUndefined();
    });
});
