import { describe, expect, it } from 'vitest';
import type { QueryClient } from '@tanstack/react-query';
import { renderSSR } from '@/test/ssr';
import zhHans from '@/locales/zh_hans.json';
import { balanceLedgerQueryOptions } from '@/api/wallet';
import Wallet from './index';

// 钱包页余额流水: 类型列原样渲染 item.kind, 缺 wallet.kind.<kind> 时会直接显示路径串。
// 这个测试把退款与提现四态渲染出来, 断言拿到的是中文文案而不是 wallet.kind.refund。

function seed(client: QueryClient) {
    client.setQueryData(balanceLedgerQueryOptions({ kind: '', userId: 0, limit: 50 }).queryKey, {
        items: [
            { id: 1, user_id: 7, kind: 'refund', amount: 6.5, frozen: 0, cost: 0, balance_after: 10, request_id: 0, model_name: '', actor_id: 0, actor_name: '', note: '申诉退款', created_at: '2026-01-01T00:00:00Z' },
            { id: 2, user_id: 7, kind: 'withdraw_unfreeze', amount: 3, frozen: 0, cost: 0, balance_after: 13, request_id: 0, model_name: '', actor_id: 0, actor_name: '', note: '提现取消', created_at: '2026-01-01T00:00:00Z' },
        ],
        total: 2,
    });
}

describe('钱包页余额流水类型文案(mock SSR)', () => {
    it('退款与提现解冻渲染中文文案, 不是原始 i18n 路径', () => {
        const html = renderSSR(<Wallet />, seed, { locale: 'zh-Hans', messages: zhHans });
        expect(html).toContain('退款返还');
        expect(html).toContain('提现解冻');
        expect(html).not.toContain('wallet.kind.');
    });
});
