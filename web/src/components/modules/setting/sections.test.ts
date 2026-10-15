import { describe, expect, it } from 'vitest';
import { SETTING_CARD_IDS, SETTING_SECTIONS, SETTING_VIEW_IDS, sectionsFor, type SettingCardID, type SettingViewID } from './sections';

// 设置页二级菜单的分组定义必须覆盖全部内容, 否则新卡片/新视图会静默消失。
describe('setting sections', () => {
    it('covers every card exactly once', () => {
        const grouped: SettingCardID[] = SETTING_SECTIONS.flatMap((section) => section.cards);
        expect(grouped.slice().sort()).toEqual(SETTING_CARD_IDS.slice().sort());
        expect(new Set(grouped).size).toBe(grouped.length);
    });

    it('covers every management view exactly once', () => {
        const grouped: SettingViewID[] = SETTING_SECTIONS.flatMap((section) => section.views);
        expect(grouped.slice().sort()).toEqual(SETTING_VIEW_IDS.slice().sort());
        expect(new Set(grouped).size).toBe(grouped.length);
    });

    it('keeps section ids unique', () => {
        const ids = SETTING_SECTIONS.map((section) => section.id);
        expect(new Set(ids).size).toBe(ids.length);
    });

    it('gives every section exactly one kind of content', () => {
        for (const section of SETTING_SECTIONS) {
            expect(section.cards.length + section.views.length).toBeGreaterThan(0);
            expect(section.cards.length === 0 || section.views.length === 0).toBe(true);
        }
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
