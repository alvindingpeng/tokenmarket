import { describe, expect, it } from 'vitest';
import en from '@/locales/en.json';
import zhHans from '@/locales/zh_hans.json';
import zhHant from '@/locales/zh_hant.json';

// task-45(前端可用性: 失败态伪装成 0/空/成功)新增的文案键。
//
// 这些键由 lead 独占写入 locales, 我这边只负责引用 —— 所以这里做一道回归闸:
// 任何一个键在三语里缺失, 界面上会直接显示 i18n 路径串(例如 ops.budgets.deleteConfirm),
// 而那种串在删除确认这种位置上, 会把一次破坏性操作变成一行看不懂的英文路径。

const NEEDED = [
    'ops.budgets.loadFailed',
    'ops.budgets.staleWarning',
    'ops.budgets.deleteConfirm',
    'ops.budgets.confirmDelete',
    'ops.budgets.confirmCancel',
    'ops.budgets.disableConfirm',
    'ops.budgets.enableConfirm',
    'ops.budgets.confirmDisable',
    'ops.budgets.confirmEnable',
    'ops.silence.staleWarning',
    'ops.silence.loadFailed',
    'ops.upstream.empty',
    'ops.upstream.loadFailed',
    'wallet.summaryFailed',
    'wallet.withdraw.minAmountHint',
    'wallet.withdraw.belowMin',
    'wallet.withdraw.belowAvailable',
    'wallet.withdraw.amountInvalid',
    'wallet.withdraw.tooManyDecimals',
    'setting.saveFailedReverted',
    'setting.payment.partialFailed',
    'setting.payment.unsavedMark',
    'setting.payment.nothingToSave',
    'setting.payment.unsavedDismiss',
    'setting.billing.countsFailed',
    // 复用已有的通用键(不为同一句话再造一份, 免得同一个词在不同页签说法不一)。
    'common.retry',
    'setting.saveFailed',
    'setting.platform.invalidNumber',
];

// lookup 按点分路径取值; 缺任何一段都返回 undefined。
function lookup(messages: unknown, path: string): string | undefined {
    let node: unknown = messages;
    for (const part of path.split('.')) {
        if (typeof node !== 'object' || node === null) return undefined;
        node = (node as Record<string, unknown>)[part];
    }
    return typeof node === 'string' ? node : undefined;
}

// placeholders 取文案里的占位符集合(排序后拼串, 便于直接比较)。
function placeholders(text: string): string {
    return (text.match(/\{[a-zA-Z0-9_]+\}/g) ?? []).sort().join(',');
}

const LOCALES = [['en', en], ['zh_hans', zhHans], ['zh_hant', zhHant]] as const;

describe('task-45 文案键三语齐全', () => {
    for (const [locale, messages] of LOCALES) {
        it(locale + ' 里这些键都存在且非空', () => {
            const missing = NEEDED.filter((key) => {
                const value = lookup(messages, key);
                return value === undefined || value.trim() === '';
            });
            expect(missing).toEqual([]);
        });
    }

    it('英文里不带中日韩字符(否则就是漏翻译)', () => {
        const cjk = NEEDED.filter((key) => /\u4e00-\u9fff/.test(lookup(en, key) ?? ''));
        expect(cjk).toEqual([]);
    });

    it('中文里确实翻了(与英文原文不同)', () => {
        // 若某键在中文里仍是英文原文, 说明这一处没翻。
        const untranslated = NEEDED.filter((key) => lookup(zhHans, key) === lookup(en, key));
        expect(untranslated).toEqual([]);
    });

    it('带占位符的键在两种语言里占位符集合一致', () => {
        // 占位符对不上会在运行时抛 MissingValueError, 或者更糟: 少一个参数就把 {short} 原样印在界面上。
        const KEY_WITH_PLACEHOLDER = [
            'wallet.withdraw.minAmountHint',
            'wallet.withdraw.belowMin',
            'wallet.withdraw.belowAvailable',
            'setting.payment.partialFailed',
        ];
        for (const key of KEY_WITH_PLACEHOLDER) {
            expect(placeholders(lookup(zhHans, key) ?? '')).toBe(placeholders(lookup(en, key) ?? ''));
            expect(placeholders(lookup(zhHant, key) ?? '')).toBe(placeholders(lookup(en, key) ?? ''));
        }
    });
});
