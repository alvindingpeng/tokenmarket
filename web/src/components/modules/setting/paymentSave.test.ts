import { describe, expect, it } from 'vitest';
import { isFieldUnsaved, summarizeBatchSave, unsavedKeys } from './paymentSave';

// 支付配置是十几个独立设置键的批量提交。这一组盯住三件事:
//   1) 部分成功必须能说出「哪几项成了、哪几项没成」, 不能只报一句「保存失败」;
//   2) 失败处停下, 后面的不再发 —— 配置项之间有隐含依赖, 造出乱序状态更难收拾;
//   3) 未保存标记要能精确落到具体键上(界面上按 key 打标, 整表标红等于没标)。

const KEYS = ['epay_gateway_url', 'epay_pid', 'stripe_secret_key', 'recharge_rate'];

describe('summarizeBatchSave', () => {
    it('全部成功时报 done, 不报任何未保存项', () => {
        const outcome = summarizeBatchSave(KEYS, KEYS.length, '');
        expect(outcome.saved).toEqual(KEYS);
        expect(outcome.done).toBe(true);
        expect(unsavedKeys(outcome)).toEqual([]);
    });

    it('中途失败时把已成功的与没发出去的分开列', () => {
        // 第 2 项(stripe_secret_key)失败: 前两项成了, 第 3 项压根没发。
        const outcome = summarizeBatchSave(KEYS, 2, 'stripe_secret_key');
        expect(outcome.saved).toEqual(['epay_gateway_url', 'epay_pid']);
        expect(outcome.failed).toBe('stripe_secret_key');
        expect(outcome.skipped).toEqual(['recharge_rate']);
        expect(outcome.done).toBe(false);
    });

    it('第一项就失败时没有任何已成功项', () => {
        const outcome = summarizeBatchSave(KEYS, 0, 'epay_gateway_url');
        expect(outcome.saved).toEqual([]);
        expect(outcome.skipped).toEqual(['epay_pid', 'stripe_secret_key', 'recharge_rate']);
    });

    it('最后一项失败时没有后续项', () => {
        const outcome = summarizeBatchSave(KEYS, 3, 'recharge_rate');
        expect(outcome.saved).toEqual(['epay_gateway_url', 'epay_pid', 'stripe_secret_key']);
        expect(outcome.skipped).toEqual([]);
    });
});

describe('unsavedKeys', () => {
    it('列出失败项与所有没发出去的项', () => {
        expect(unsavedKeys(summarizeBatchSave(KEYS, 1, 'epay_pid'))).toEqual([
            'epay_pid', 'stripe_secret_key', 'recharge_rate',
        ]);
    });

    it('全部成功时返回空数组, 不产生空串标记', () => {
        // failed 在成功时是 '', 若直接拼进数组会得到 [''] —— 一个"未保存列表里有一个元素"的假象。
        expect(unsavedKeys(summarizeBatchSave(KEYS, KEYS.length, ''))).toEqual([]);
    });
});

describe('isFieldUnsaved', () => {
    it('从未保存过时不标任何格子', () => {
        expect(isFieldUnsaved(null, 'epay_pid')).toBe(false);
    });

    it('只标失败项与没发出去的项, 已成功的不标', () => {
        // 已成功项如果也被标红, 用户会重新保存一遍已经落库的值。
        const outcome = summarizeBatchSave(KEYS, 2, 'stripe_secret_key');
        expect(isFieldUnsaved(outcome, 'stripe_secret_key')).toBe(true);
        expect(isFieldUnsaved(outcome, 'recharge_rate')).toBe(true);
        expect(isFieldUnsaved(outcome, 'epay_pid')).toBe(false);
    });

    it('不做模糊匹配: 前缀相同的已成功项不会被误标', () => {
        // epay_pid 成功、epay_key 失败 —— 用 startsWith/includes 判断就会两颗都红。
        const outcome = summarizeBatchSave(KEYS, 2, 'stripe_secret_key');
        expect(isFieldUnsaved(outcome, 'epay_gateway_url')).toBe(false);
        expect(isFieldUnsaved(outcome, 'epay')).toBe(false);
    });

    it('全部成功后不再标任何格子(标记随成功清除)', () => {
        const outcome = summarizeBatchSave(KEYS, KEYS.length, '');
        expect(isFieldUnsaved(outcome, 'stripe_secret_key')).toBe(false);
    });
});
