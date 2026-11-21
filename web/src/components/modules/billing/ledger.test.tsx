import { describe, expect, it } from 'vitest';
import type { QueryClient } from '@tanstack/react-query';
import { renderSSR } from '@/test/ssr';
import zhHans from '@/locales/zh_hans.json';
import { balanceLedgerQueryOptions, type LedgerRow } from '@/api/wallet';
import { LEDGER_KINDS } from '@/lib/ledger-kinds';
import { BalanceLedgerTable } from './index';

// 回归: 计费页余额流水表格此前只认 4 种计费类, 退款与提现四态会以
// billing.ledger.kind.refund 这种原始 i18n 路径显示在表里(use-intl 缺键回退)。
// 这里用 mock SSR 把一行退款与一行提现打款渲染出来, 断言它们显示的是**中文文案**而非路径串。

const row = (patch: Partial<LedgerRow>): LedgerRow => ({
    id: 1,
    user_id: 7,
    kind: 'settle',
    amount: -1,
    frozen: 0,
    cost: 1,
    balance_after: 10,
    request_id: 0,
    model_name: '',
    actor_id: 0,
    actor_name: '',
    note: '',
    created_at: '2026-01-01T00:00:00Z',
    ...patch,
});

const rows: LedgerRow[] = [
    row({ id: 1, kind: 'refund', amount: 6.5, note: '申诉退款' }),
    row({ id: 2, kind: 'withdraw_freeze', amount: -3, note: '提现冻结' }),
    row({ id: 3, kind: 'withdraw_paid', amount: 0, note: '提现打款' }),
    row({ id: 4, kind: 'withdraw_rollback', amount: 0, note: '打款回滚' }),
    row({ id: 5, kind: 'bonus', amount: 1, note: '充值满赠' }),
];

function seed(client: QueryClient) {
    // BalanceLedgerTable 首屏用 kind='' 查询, 键与组件取数同一套定义。
    client.setQueryData(balanceLedgerQueryOptions({ kind: '', limit: 20, offset: 0 }).queryKey, {
        items: rows,
        total: rows.length,
    });
}

describe('余额流水表格的 12 种类型(mock SSR)', () => {
    it('退款与提现类渲染中文文案, 不是原始 i18n 路径', () => {
        const html = renderSSR(<BalanceLedgerTable />, seed, { locale: 'zh-Hans', messages: zhHans });
        for (const text of ['退款返还', '提现冻结', '提现已打款', '提现打款回滚', '满赠']) {
            expect(html).toContain(text);
        }
        // 缺键的典型症状就是页面直接显示 "billing.ledger.kind.xxx"。
        expect(html).not.toContain('ledger.kind.');
    });

    it('筛选下拉用的是全量类型(断言常量, 不依赖 SSR 里未展开的门户)', () => {
        // Radix Select 的选项在 Portal 里, 服务端渲染只出触发器, 拿不到选项文本。
        // 因此这里断言数据源本身: 下拉由 ledgerKindOptions 驱动, 与 Go 全集一致。
        // 每条都要有文案(缺文案时下拉项会显示 billing.ledger.kind.xxx)。
        const ledgerKind = (zhHans as unknown as { billing: { ledger: { kind: Record<string, string> } } }).billing.ledger.kind;
        for (const kind of LEDGER_KINDS) {
            expect({ kind, value: ledgerKind[kind] }).toEqual({ kind, value: expect.any(String) });
            expect(ledgerKind[kind].trim()).not.toBe('');
        }
    });

    it('空列表走空态而不是空白表格', () => {
        const html = renderSSR(<BalanceLedgerTable />, (client) => {
            client.setQueryData(balanceLedgerQueryOptions({ kind: '', limit: 20, offset: 0 }).queryKey, { items: [], total: 0 });
        }, { locale: 'zh-Hans', messages: zhHans });
        expect(html).toContain('暂无余额流水');
    });
});