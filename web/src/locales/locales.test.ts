import { describe, expect, it } from 'vitest';
import en from './en.json';
import zhHans from './zh_hans.json';
import zhHant from './zh_hant.json';

// 三语文案必须键位完全一致。
// use-intl 在缺键时会直接抛错, 表现为整页白屏 —— 而缺键往往只是「加了中文忘了补另两种语言」。
// 这个测试把「忘补」拦在提交前, 而不是等用户切到繁体才发现。
type Tree = { [key: string]: string | Tree };

function flatten(tree: Tree, prefix = ''): Map<string, string> {
    const out = new Map<string, string>();
    for (const [key, value] of Object.entries(tree)) {
        const path = prefix ? prefix + '.' + key : key;
        if (typeof value === 'string') out.set(path, value);
        else for (const [k, v] of flatten(value, path)) out.set(k, v);
    }
    return out;
}

const locales: [string, Map<string, string>][] = [
    ['zh_hans', flatten(zhHans as Tree)],
    ['zh_hant', flatten(zhHant as Tree)],
    ['en', flatten(en as Tree)],
];

describe('locales', () => {
    it('三种语言键位完全一致', () => {
        const base = locales[0][1];
        for (const [name, keys] of locales.slice(1)) {
            const missing = [...base.keys()].filter((key) => !keys.has(key));
            const extra = [...keys.keys()].filter((key) => !base.has(key));
            expect({ locale: name, missing, extra }).toEqual({ locale: name, missing: [], extra: [] });
        }
    });

    it('没有空文案', () => {
        // share.reviewGate.approved 是刻意留空的: 审核通过时不构成拦截, 没有话要说。
        // 保留这个键是为了让 t('reviewGate.' + status) 对任何状态都取得到值(缺键会抛错),
        // 实际渲染处也有 publishBlocked 守卫, 该状态根本不会走到这段文案。
        const ALLOWED_EMPTY = new Set(['share.reviewGate.approved']);
        for (const [name, keys] of locales) {
            const empty = [...keys.entries()]
                .filter(([key, value]) => value.trim() === '' && !ALLOWED_EMPTY.has(key))
                .map(([key]) => key);
            expect({ locale: name, empty }).toEqual({ locale: name, empty: [] });
        }
    });

    it('插值占位符在各语言中保持一致', () => {
        // 少写一个 {seconds} 之类的占位符不会报错, 只会在界面上少显示一个数字。
        const placeholders = (value: string) => (value.match(/\{\w+\}/g) ?? []).sort().join(',');
        const base = locales[0][1];
        for (const [name, keys] of locales.slice(1)) {
            const mismatched = [...base.entries()]
                .filter(([key, value]) => keys.has(key) && placeholders(value) !== placeholders(keys.get(key) as string))
                .map(([key]) => key);
            expect({ locale: name, mismatched }).toEqual({ locale: name, mismatched: [] });
        }
    });
});
