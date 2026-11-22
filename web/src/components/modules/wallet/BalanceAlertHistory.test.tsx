import { describe, expect, it } from 'vitest';
import { renderSSR } from '@/test/ssr';
import { BalanceAlertHistory } from './BalanceAlertHistory';
import en from '@/locales/en.json';

const key = ['balance-alert', 'history'] as const;

describe('BalanceAlertHistory', () => {
    it('loading does not masquerade as empty history', () => {
        const html = renderSSR(<BalanceAlertHistory />);
        expect(html).toContain(en.wallet.alert.historyLoading);
        expect(html).not.toContain(en.wallet.alert.historyEmpty);
    });

    it('error renders retry, not empty history', () => {
        const html = renderSSR(<BalanceAlertHistory />, (client) => {
            client.setDefaultOptions({ queries: { retryOnMount: false, retry: false } });
            client.getQueryCache().build(client, { queryKey: key }, {
                data: undefined, dataUpdateCount: 0, dataUpdatedAt: 0,
                error: new Error('boom'), errorUpdateCount: 1, errorUpdatedAt: Date.now(),
                fetchFailureCount: 1, fetchFailureReason: new Error('boom'), fetchMeta: null,
                isInvalidated: false, status: 'error', fetchStatus: 'idle',
            });
        });
        expect(html).toContain(en.wallet.alert.historyError);
        expect(html).toContain(en.wallet.alert.historyRetry);
        expect(html).not.toContain(en.wallet.alert.historyEmpty);
    });

    it('empty array uses explicit empty state', () => {
        const html = renderSSR(<BalanceAlertHistory />, (client) => client.setQueryData(key, { items: [] }));
        expect(html).toContain(en.wallet.alert.historyEmpty);
    });

    it('renders only the recorded timestamp and source', () => {
        const html = renderSSR(<BalanceAlertHistory />, (client) => client.setQueryData(key, {
            items: [{ triggered_at: '2026-11-22T00:00:00Z', source: 'balance_alert' }],
        }));
        expect(html).toContain(en.wallet.alert.historyTime);
        expect(html).toContain('balance_alert');
        expect(html).toContain('2026-11-22');
        expect(html).not.toContain('threshold');
        expect(html).not.toContain('available');
    });
});
