import { describe, expect, it } from 'vitest';
import { SETTING_CARD_IDS, SETTING_SECTIONS, sectionsFor, type SettingCardID } from './sections';

// 设置页二级菜单的分组定义必须覆盖全部卡片, 否则新卡片会静默消失。
describe('setting sections', () => {
    it('covers every card exactly once', () => {
        const grouped: SettingCardID[] = SETTING_SECTIONS.flatMap((section) => section.cards);
        expect(grouped.slice().sort()).toEqual(SETTING_CARD_IDS.slice().sort());
        expect(new Set(grouped).size).toBe(grouped.length);
    });

    it('keeps section ids unique', () => {
        const ids = SETTING_SECTIONS.map((section) => section.id);
        expect(new Set(ids).size).toBe(ids.length);
    });

    it('hides admin-only sections from ordinary users', () => {
        const userSections = sectionsFor('user');
        expect(userSections.length).toBeGreaterThan(0);
        expect(userSections.every((section) => !section.adminOnly)).toBe(true);
        expect(sectionsFor('admin').length).toBe(SETTING_SECTIONS.length);
    });

    it('keeps the personal section available to everyone', () => {
        for (const role of ['admin', 'reseller', 'user', null]) {
            expect(sectionsFor(role).some((section) => section.id === 'personal')).toBe(true);
        }
    });
});
