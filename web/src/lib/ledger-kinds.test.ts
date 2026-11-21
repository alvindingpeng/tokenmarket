import { describe, expect, it } from 'vitest';
import en from '@/locales/en.json';
import zhHans from '@/locales/zh_hans.json';
import zhHant from '@/locales/zh_hant.json';
import { LEDGER_KINDS, ledgerKindOptions, ledgerKindTone } from './ledger-kinds';

// 这份清单手工抄自 Go 侧常量, 刻意不读文件: 测试直接 import Go 源码既需要 fs 解析,
// 也会在打包/CI 的路径差异下失效。写死的好处是 Go 侧新增 kind 时这里必须有人来改,
// 否则下面的集合断言会红 —— 比「静默漏翻、界面显示原始 i18n 路径」好得多。
//
// 来源: internal/model/ledger.go:6-21 (const LedgerKind* = "...")。
const GO_LEDGER_KINDS = [
    'reserve', // LedgerKindReserve
    'settle', // LedgerKindSettle
    'release', // LedgerKindRelease
    'adjust', // LedgerKindAdjust
    'topup', // LedgerKindTopup
    'redemption', // LedgerKindRedeem
    'bonus', // LedgerKindBonus
    'refund', // LedgerKindRefund
    'withdraw_freeze', // LedgerKindWithdrawFreeze
    'withdraw_unfreeze', // LedgerKindWithdrawUnfreeze
    'withdraw_paid', // LedgerKindWithdrawPaid
    'withdraw_rollback', // LedgerKindWithdrawRollback
] as const;

const locales: [string, Record<string, unknown>][] = [
    ['zh_hans', zhHans as unknown as Record<string, unknown>],
    ['zh_hant', zhHant as unknown as Record<string, unknown>],
    ['en', en as unknown as Record<string, unknown>],
];

describe('LEDGER_KINDS 与后端常量一致', () => {
    it('12 种 kind 一个不多一个不少', () => {
        expect([...LEDGER_KINDS].sort()).toEqual([...GO_LEDGER_KINDS].sort());
        expect(LEDGER_KINDS).toHaveLength(12);
    });

    it('筛选项 = 全部 + 12 种, 空串代表全部', () => {
        expect(ledgerKindOptions[0]).toBe('');
        expect(ledgerKindOptions).toHaveLength(13);
        expect([...ledgerKindOptions.slice(1)].sort()).toEqual([...GO_LEDGER_KINDS].sort());
    });
});

describe('三语 kind 文案覆盖全部 12 种', () => {
    // 钱包页用 wallet.kind.<kind>, 计费页用 billing.ledger.kind.<kind>。
    // 两个 namespace 的键集合都必须正好等于 Go 全集: 少一个就是界面上的原始路径串,
    // 多一个则是没有对应流水的死键(通常是改名残留)。
    const namespaces: [string, (messages: Record<string, unknown>) => Record<string, unknown>][] = [
        ['wallet.kind', (messages) => ((messages.wallet as Record<string, unknown>).kind as Record<string, unknown>)],
        ['billing.ledger.kind', (messages) => (((messages.billing as Record<string, unknown>).ledger as Record<string, unknown>).kind as Record<string, unknown>),],
    ];

    for (const [namespace, pick] of namespaces) {
        it(namespace + ' 键集合 == Go kind 全集', () => {
            for (const [name, messages] of locales) {
                const kind = pick(messages);
                expect({ name, keys: Object.keys(kind).sort() }).toEqual({ name, keys: [...GO_LEDGER_KINDS].sort() });
            }
        });

        it(namespace + ' 每条文案非空且无残留中文键名', () => {
            for (const [name, messages] of locales) {
                const kind = pick(messages) as Record<string, string>;
                for (const value of Object.values(kind)) {
                    expect({ name, value }).toEqual({ name, value: expect.any(String) });
                    expect({ name, value }).not.toEqual({ name, value: '' });
                }
            }
        });
    }

    it('英文文案不得残留中文', () => {
        const kind = ((en as unknown as Record<string, unknown>).wallet as Record<string, unknown>).kind as Record<string, string>;
        const ledger = (((en as unknown as Record<string, unknown>).billing as Record<string, unknown>).ledger as Record<string, unknown>).kind as Record<string, string>;
        for (const value of [...Object.values(kind), ...Object.values(ledger)]) {
            expect(value).not.toMatch(/[一-鿿]/);
        }
    });
});

describe('毛利看板退款/充值卡片文案', () => {
    // 退款与充值两张卡此前只有类型字段、没有渲染。这里守住的不是"有键", 而是
    // 键里有口径提示: 文案若被改回「累计退款/现金充值」这种含糊说法, 用户会把它们读成毛利的一部分。
    const marginOf = (messages: Record<string, unknown>) => ((messages.billing as Record<string, unknown>).margin as Record<string, unknown>);

    it('三语都补齐退款/充值卡片与口径提示键', () => {
        for (const [name, messages] of locales) {
            const margin = marginOf(messages) as Record<string, string>;
            for (const key of ['refundAmount', 'rechargeAmount', 'refundHint', 'rechargeHint', 'flowNote']) {
                expect({ name, key, value: margin[key] }).toEqual({ name, key, value: expect.any(String) });
                expect({ name, key, value: (margin[key] ?? '').trim() }).not.toEqual({ name, key, value: '' });
            }
        }
    });

    it('口径提示明确写出不构成等式 / 净额', () => {
        // 退款按批准日、净额按账单日扣减, 跨区间时两者不等 —— 文案里必须留痕,
        // 否则运维会拿 refund_amount 去核对 GMV 差值, 发现对不上再兜一圈。
        const hans = marginOf(zhHans as unknown as Record<string, unknown>) as Record<string, string>;
        const hant = marginOf(zhHant as unknown as Record<string, unknown>) as Record<string, string>;
        const english = marginOf(en as unknown as Record<string, unknown>) as Record<string, string>;
        expect(hans.flowNote).toContain('净额');
        expect(hant.flowNote).toContain('淨額');
        expect(english.flowNote.toLowerCase()).toContain('net');
    });
});
describe('ledgerKindTone 覆盖全部 12 种', () => {
    // 表格徽章配色按资金方向分类: 没有配色的类型会退化成默认灰徽章, 一眼看不出是进账还是出账。
    // 这里只断言"每种都有配色且非空", 具体色号是视觉取舍, 不该被测试钉死。
    it('每种 kind 都有配色', () => {
        for (const kind of LEDGER_KINDS) {
            expect({ kind, tone: ledgerKindTone(kind) }).toEqual({ kind, tone: expect.any(String) });
            expect(ledgerKindTone(kind).length).toBeGreaterThan(0);
        }
    });

    it('未知 kind 不抛错, 返回空串走默认样式', () => {
        expect(ledgerKindTone('something_new')).toBe('');
        expect(ledgerKindTone('')).toBe('');
    });

    it('入账与扣费用不同色系: 同名色系混用会让表格无法扫读', () => {
        expect(ledgerKindTone('settle')).not.toBe(ledgerKindTone('topup'));
        expect(ledgerKindTone('refund')).not.toBe(ledgerKindTone('withdraw_paid'));
    });
});
