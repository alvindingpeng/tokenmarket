import { describe, expect, it } from 'vitest';
import { amountWithPoolCurrency, poolCurrencyOf } from './poolCurrency';

// poolCurrencyOf / amountWithPoolCurrency 的单测。
// 组件级断言在 poolCurrency.test.tsx; 这里钉的是「怎么读那个字段」——
// 因为字段来自别的服务的 JSON, 脏数据(数字/null/小写/带空白)不该被当成币种展示在资金页上。

describe('poolCurrencyOf', () => {
    it('取到字符串并统一大写去空白', () => {
        expect(poolCurrencyOf({ pool_currency: 'USD' })).toBe('USD');
        expect(poolCurrencyOf({ pool_currency: ' usd ' })).toBe('USD');
        expect(poolCurrencyOf({ pool_currency: 'eur' })).toBe('EUR');
    });

    it('字段缺失时返回空串, 不兜底成任何币种', () => {
        // 兜底成 CNY 就是把美元当人民币展示 —— 比不显示更糟; 兜底成 USD 则是在猜后端。
        expect(poolCurrencyOf({})).toBe('');
        expect(poolCurrencyOf(undefined)).toBe('');
        expect(poolCurrencyOf(null)).toBe('');
    });

    it('非字符串脏数据一律当作未下发', () => {
        // 后端换实现/旧版本可能回 null 或数字; 把 123 当币种打印出来只会更让人困惑。
        expect(poolCurrencyOf({ pool_currency: null })).toBe('');
        expect(poolCurrencyOf({ pool_currency: 123 })).toBe('');
        expect(poolCurrencyOf({ pool_currency: '' })).toBe('');
        expect(poolCurrencyOf({ pool_currency: '   ' })).toBe('');
        expect(poolCurrencyOf('USD')).toBe('');
    });
});

describe('amountWithPoolCurrency', () => {
    it('有币种时拼成「金额 空格 代码」', () => {
        expect(amountWithPoolCurrency('1,234.5', 'USD')).toBe('1,234.5 USD');
        expect(amountWithPoolCurrency('0', 'USD')).toBe('0 USD');
    });

    it('无币种时只留金额, 不加空格也不留尾巴', () => {
        // 金额本身仍是可靠事实: 单位缺失不能让数字消失, 更不能退化成 0。
        expect(amountWithPoolCurrency('1,234.5', '')).toBe('1,234.5');
        expect(amountWithPoolCurrency('0', '')).toBe('0');
    });
});
