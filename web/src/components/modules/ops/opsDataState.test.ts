import { describe, expect, it } from 'vitest';
import { budgetRowMode, opsDataState } from './opsDataState';

// 这一组守的是「失败态不许伪装成空态」这条前端契约在三处运维区块里的落地判定。

describe('opsDataState', () => {
    it('失败且无旧数据时报 error(不是 empty)', () => {
        // 这是原本的缺陷: 只判 isLoading, 失败时 count 是 0, 落进空态,
        // 屏幕写「还没有建过预算」—— 管理员据此以为预算被人删光了。
        expect(opsDataState({ isError: true, isLoading: false, count: 0 })).toBe('error');
    });

    it('失败但有旧数据时报 stale(保留旧数据 + 提示可能过期)', () => {
        expect(opsDataState({ isError: true, isLoading: false, count: 3 })).toBe('stale');
    });

    it('重试中(isError 与 isLoading 同为真)仍报失败态, 不回到转圈', () => {
        // 转圈会让人以为还在努力, 而其实上一次已经失败了。
        expect(opsDataState({ isError: true, isLoading: true, count: 0 })).toBe('error');
        expect(opsDataState({ isError: true, isLoading: true, count: 2 })).toBe('stale');
    });

    it('首次加载中报 loading', () => {
        expect(opsDataState({ isError: false, isLoading: true, count: 0 })).toBe('loading');
    });

    it('真没有数据时才是 empty', () => {
        expect(opsDataState({ isError: false, isLoading: false, count: 0 })).toBe('empty');
    });

    it('有数据时报 ready', () => {
        expect(opsDataState({ isError: false, isLoading: false, count: 1 })).toBe('ready');
    });
});

describe('budgetRowMode', () => {
    it('没有待确认项时所有行都是 idle', () => {
        expect(budgetRowMode(null, 1)).toBe('idle');
        expect(budgetRowMode(null, 99)).toBe('idle');
    });

    it('确认态精确到行, 不波及别的行', () => {
        // 用布尔的话, 点第 2 行时第 1 行的确认态也会亮 —— 那时候按下去删掉的是第 1 条。
        const pending = { id: 2, kind: 'delete' as const };
        expect(budgetRowMode(pending, 2)).toBe('confirmDelete');
        expect(budgetRowMode(pending, 1)).toBe('idle');
    });

    it('启停与删除区分开', () => {
        expect(budgetRowMode({ id: 7, kind: 'toggle' }, 7)).toBe('confirmToggle');
        expect(budgetRowMode({ id: 7, kind: 'delete' }, 7)).toBe('confirmDelete');
    });
});
