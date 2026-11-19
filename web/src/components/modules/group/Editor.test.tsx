import type { ComponentProps } from 'react';
import { describe, expect, it, vi } from 'vitest';
import type { GroupMode } from '@/api/group';
import { TooltipProvider } from '@/components/ui/tooltip';
import { renderSSR } from '@/test/ssr';
import en from '@/locales/en.json';
import zhHans from '@/locales/zh_hans.json';
import zhHant from '@/locales/zh_hant.json';
import { GroupEditor } from './Editor';

// Open the real routing panel for SSR without a browser; all fields remain real controls.
vi.mock('@/components/ui/tabs', async (importOriginal) => {
    const actual = await importOriginal<typeof import('@/components/ui/tabs')>();
    return {
        ...actual,
        Tabs: (props: ComponentProps<typeof actual.Tabs>) => <actual.Tabs {...props} defaultValue="relay" />,
    };
});

function renderEditor(mode: GroupMode, messages = en, locale = 'en') {
    return renderSSR(
        <TooltipProvider>
            <GroupEditor
                initial={{ mode, relay_config: { max_total_attempts: 5, member_max_attempts: 7, member_retry_interval_seconds: 9 } }}
                submitText="Save"
                submittingText="Saving"
                isSubmitting={false}
                onSubmit={() => {}}
            />
        </TooltipProvider>,
        undefined,
        { messages, locale },
    );
}

function input(html: string, id: string) {
    const tag = html.match(new RegExp('<input[^>]*id="' + id + '"[^>]*>'))?.[0];
    expect(tag, id).toBeDefined();
    return tag!;
}

describe('GroupEditor score retry controls (SSR)', () => {
    it.each([
        ['en', en],
        ['zh-Hans', zhHans],
        ['zh-Hant', zhHant],
    ])('shows explicit score-only retry guidance in %s', (locale, messages) => {
        const html = renderEditor('score', messages, locale);
        expect(html).toContain(messages.group.form.scoreRetryHint);
        expect(input(html, 'group-max_total_attempts')).toContain('value="5"');
        expect(input(html, 'group-max_total_attempts')).not.toContain('disabled=""');
    });

    it('disables member retries and interval without replacing saved values', () => {
        const html = renderEditor('score');
        expect(input(html, 'group-retry-count')).toContain('disabled=""');
        expect(input(html, 'group-retry-count')).toContain('value="7"');
        expect(input(html, 'group-retry-interval')).toContain('disabled=""');
        expect(input(html, 'group-retry-interval')).toContain('value="9"');
    });

    it.each(['failover', 'price', 'latency', 'success', 'random'] as const)(
        'leaves %s member retry controls and saved values unchanged', (mode) => {
            const html = renderEditor(mode);
            expect(html).not.toContain(en.group.form.scoreRetryHint);
            expect(input(html, 'group-retry-count')).not.toContain('disabled=""');
            expect(input(html, 'group-retry-count')).toContain('value="7"');
            expect(input(html, 'group-retry-interval')).not.toContain('disabled=""');
            expect(input(html, 'group-retry-interval')).toContain('value="9"');
        },
    );

    it('keeps manual mode member-attempt control hidden and interval enabled', () => {
        const html = renderEditor('manual');
        expect(html).not.toContain('id="group-retry-count"');
        expect(input(html, 'group-retry-interval')).not.toContain('disabled=""');
        expect(html).not.toContain(en.group.form.scoreRetryHint);
    });
});

describe('retry explanation locale contract', () => {
    it.each([
        [en, 'including the first request', 'at most 5 different candidates', 'never returns'],
        [zhHans, '包含首次请求', '最多尝试 5 个不同候选', '不会回头重试'],
        [zhHant, '包含首次請求', '最多嘗試 5 個不同候選', '不會回頭重試'],
    ])('counts the first attempt and explains distinct score candidates', (messages, first, limit, noReturn) => {
        expect(messages.group.form.max_total_attemptsHint).toContain(first);
        expect(messages.group.form.scoreRetryHint).toContain(limit);
        expect(messages.group.form.scoreRetryHint).toContain(noReturn);
    });
});
