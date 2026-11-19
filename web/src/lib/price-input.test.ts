import { describe, expect, it } from 'vitest';
import { MAX_LLM_PRICE_PER_MILLION, parseLLMPriceForm, parsePriceInput } from './price-input';

describe('parsePriceInput', () => {
    it('空串按 0 处理: 免费模型是合法配置', () => {
        expect(parsePriceInput('')).toEqual({ ok: true, value: 0 });
        expect(parsePriceInput('   ')).toEqual({ ok: true, value: 0 });
        expect(parsePriceInput(undefined)).toEqual({ ok: true, value: 0 });
        expect(parsePriceInput(null)).toEqual({ ok: true, value: 0 });
    });

    it('接受真实单价', () => {
        expect(parsePriceInput('0.15')).toEqual({ ok: true, value: 0.15 });
        expect(parsePriceInput('15')).toEqual({ ok: true, value: 15 });
        expect(parsePriceInput('0')).toEqual({ ok: true, value: 0 });
    });

    it('拒绝负价: 负价会让 Cost 算出负费用, 即调用反而加钱', () => {
        expect(parsePriceInput('-1')).toEqual({ ok: false, reason: 'negative' });
        expect(parsePriceInput('-0.001')).toEqual({ ok: false, reason: 'negative' });
    });

    it('拒绝非数字: 旧实现 parseFloat(x) || 0 会把它静默变成 0(付费模型变免费)', () => {
        expect(parsePriceInput('abc')).toEqual({ ok: false, reason: 'not_a_number' });
        // parseFloat 只吃前缀, 会把 1.2.3 读成 1.2 并当成功 —— Number 不会。
        expect(parsePriceInput('1.2.3')).toEqual({ ok: false, reason: 'not_a_number' });
        expect(parsePriceInput('12abc')).toEqual({ ok: false, reason: 'not_a_number' });
        expect(parsePriceInput('Infinity')).toEqual({ ok: false, reason: 'not_a_number' });
    });

    it('拒绝量级离谱的单价: 常见事故是把每千 token 价填进每百万的框, 差 1000 倍', () => {
        expect(parsePriceInput(String(MAX_LLM_PRICE_PER_MILLION + 1))).toEqual({ ok: false, reason: 'too_large' });
        expect(parsePriceInput(String(MAX_LLM_PRICE_PER_MILLION))).toEqual({ ok: true, value: MAX_LLM_PRICE_PER_MILLION });
    });
});

describe('parseLLMPriceForm', () => {
    it('四项都合法时返回数值表', () => {
        const result = parseLLMPriceForm({ input: '0.15', output: '0.6', cache_read: '0.015', cache_write: '' });
        expect(result).toEqual({ ok: true, fields: { input: 0.15, output: 0.6, cache_read: 0.015, cache_write: 0 } });
    });

    it('任一项非法即整体拒绝, 不做部分提交', () => {
        const result = parseLLMPriceForm({ input: '0.15', output: '-2', cache_read: '', cache_write: '' });
        expect(result).toEqual({ ok: false, reason: 'negative' });
    });
});
