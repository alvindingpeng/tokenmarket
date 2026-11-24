import { describe, expect, it } from 'vitest';
import type { SelectedMember } from './ItemList';
import { autoGrantIDsValue, compareContextOverrides, contextOverridesValue, parseAutoGrantIDs, parseContextOverrides, toggleAutoGrantID } from './autoRouting';

const member = (id: number): SelectedMember => ({
    id: String(id),
    channel_grant_id: id,
    name: `model-${id}`,
    enabled: true,
    channel_id: id,
    channel_name: `channel-${id}`,
    key_name: 'key',
    protocols: 0,
});

describe('auto routing context overrides', () => {
    it('drops malformed keys and preserves explicit values', () => {
        expect(parseContextOverrides({ '653': 10000, bad: 2, '0': 4, '-1': 5 })).toEqual({ 653: 10000 });
        expect(contextOverridesValue({ 653: 10000, 0: 4 })).toEqual({ '653': 10000 });
    });

    it('compares key order without reporting false edits', () => {
        expect(compareContextOverrides({ '653': 10000, '476': 20000 }, { '476': 20000, '653': 10000 })).toBe(false);
        expect(compareContextOverrides({ '653': 10000 }, { '653': 20000 })).toBe(true);
    });
});

describe('auto routing candidate selection', () => {
    it('keeps saved comma-separated grant IDs backward compatible', () => {
        expect(parseAutoGrantIDs(' 12, 7,12,invalid,-1,0 ')).toEqual([12, 7]);
    });

    it('writes only selected members in group priority order', () => {
        const members = [member(3), member(1), member(2)];
        expect(autoGrantIDsValue(members, [2, 9, 1])).toBe('1,2');
    });

    it('toggles membership without disturbing other task candidates', () => {
        expect(toggleAutoGrantID([1, 3], 2, true)).toEqual([1, 3, 2]);
        expect(toggleAutoGrantID([1, 2, 3], 2, false)).toEqual([1, 3]);
    });
});
