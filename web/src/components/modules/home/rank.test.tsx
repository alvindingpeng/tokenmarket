import { describe, expect, it } from 'vitest';
import { rankItems, type RankItem } from './rank';




// 榜单排序回归: 顺序必须跟随当前维度(金额/次数/词元)。
// fixture 刻意让三个维度的第一名不同: gpt-b 赢金额和次数, gpt-a 赢词元 ——
// 若排序读错字段, 词元用例会立刻翻车。
const mkMetrics = (cost: number, tokens: number, count: number) => ({
    total_cost: { raw: cost, formatted: { value: String(cost), unit: '$' } },
    total_token: { raw: tokens, formatted: { value: String(tokens), unit: '' } },
    request_count: { raw: count, formatted: { value: String(count), unit: '' } },
    request_success: { raw: count, formatted: { value: String(count), unit: '' } },
    request_failed: { raw: 0, formatted: { value: '0', unit: '' } },
});

describe('rankItems 排序跟随维度', () => {
    const items: RankItem[] = [
        { id: 'model-gpt-a', name: 'gpt-a', formatted: mkMetrics(2, 900, 5) },
        { id: 'model-gpt-b', name: 'gpt-b', formatted: mkMetrics(20, 100, 50) },
    ];
    const names = (mode: 'cost' | 'count' | 'tokens') => rankItems(items, mode).map((i) => i.name);

    it('按金额: gpt-b(20) 在 gpt-a(2) 前', () => {
        expect(names('cost')).toEqual(['gpt-b', 'gpt-a']);
    });
    it('按次数: gpt-b(50) 在 gpt-a(5) 前', () => {
        expect(names('count')).toEqual(['gpt-b', 'gpt-a']);
    });
    it('按词元: gpt-a(900) 反超到第一', () => {
        expect(names('tokens')).toEqual(['gpt-a', 'gpt-b']);
    });
});
