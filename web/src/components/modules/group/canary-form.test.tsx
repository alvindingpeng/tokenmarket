import type { ComponentProps } from 'react';
import { describe, expect, expectTypeOf, it, vi } from 'vitest';
import type { GroupMode, GroupRelayConfig } from '@/api/group';
import { TooltipProvider } from '@/components/ui/tooltip';
import { renderSSR } from '@/test/ssr';
import en from '@/locales/en.json';
import { GroupEditor } from './Editor';

// 灰度分流是叠加层而不是模式: 这块只在非手动模式出现, 也不能往模式下拉里塞新选项。
vi.mock('@/components/ui/tabs', async (importOriginal) => {
    const actual = await importOriginal<typeof import('@/components/ui/tabs')>();
    return {
        ...actual,
        Tabs: (props: ComponentProps<typeof actual.Tabs>) => <actual.Tabs {...props} defaultValue="relay" />,
    };
});

// React 会把文案里的引号转义, 直接比对原文会永远失败。
const escaped = (text: string) => text.replace(/'/g, '&#x27;');

function renderEditor(mode: GroupMode, relayConfig: Record<string, unknown> = {}) {
    return renderSSR(
        <TooltipProvider>
            <GroupEditor
                initial={{ mode, relay_config: relayConfig }}
                submitText="Save"
                submittingText="Saving"
                isSubmitting={false}
                onSubmit={() => {}}
            />
        </TooltipProvider>,
        undefined,
        { messages: en, locale: 'en' },
    );
}

function tag(html: string, pattern: RegExp) {
    const found = html.match(pattern)?.[0];
    expect(found, String(pattern)).toBeDefined();
    return found!;
}

describe('灰度分流表单 (SSR)', () => {
    it('手动模式整块不渲染', () => {
        const html = renderEditor('manual');
        expect(html).not.toContain(en.group.form.canaryTitle);
        expect(html).not.toContain('id="group-canary-percent"');
        expect(html).not.toContain('id="group-canary-users"');
        expect(html).not.toContain('id="group-canary-per-request"');
    });

    it.each(['failover', 'score', 'random'] as const)('%s 模式渲染出灰度块与说明', (mode) => {
        const html = renderEditor(mode);
        expect(html).toContain(en.group.form.canaryTitle);
        expect(html).toContain(escaped(en.group.form.canaryHint));
        expect(html).toContain(escaped(en.group.form.canaryPerRequestHint));
        expect(html).toContain('id="group-canary-percent"');
        expect(html).toContain('id="group-canary-users"');
    });

    it('未配置时是关闭态: 比例 0、用户为空、每请求随机未勾选', () => {
        const html = renderEditor('failover');
        expect(tag(html, /<input[^>]*id="group-canary-percent"[^>]*>/)).toContain('value="0"');
        expect(tag(html, /<input[^>]*id="group-canary-users"[^>]*>/)).toContain('value=""');
        // Radix Switch 用 data-state 表达勾选; 默认必须是关闭, 不能替用户打开灰度。
        expect(tag(html, /<button[^>]*data-slot="switch"[^>]*>/)).toContain('data-state="unchecked"');
    });

    it('保存的灰度值回填到表单', () => {
        const html = renderEditor('price', {
            canary_item_id: 0, canary_percent: 30, canary_user_ids: [7, 8], canary_per_request: true,
        });
        expect(tag(html, /<input[^>]*id="group-canary-percent"[^>]*>/)).toContain('value="30"');
        expect(tag(html, /<input[^>]*id="group-canary-users"[^>]*>/)).toContain('value="7, 8"');
        expect(tag(html, /<button[^>]*data-slot="switch"[^>]*>/)).toContain('data-state="checked"');
    });

    it('成员尚未落库时说明为何没有可选成员', () => {
        const html = renderEditor('failover');
        expect(html).toContain(en.group.form.canaryMemberEmpty);
        expect(html).not.toContain('id="group-canary-member"');
    });

    it('灰度没有变成模式, 也没有回落模式字段', () => {
        // 类型层面钉死: 模式集合仍是那七个, 灰度是 relay_config 上的叠加字段。
        // 谁要是把 canary 加进 GroupMode 或加一个 canary_fallback_mode, 这里先编译失败。
        expectTypeOf<GroupMode>().toEqualTypeOf<
            'manual' | 'failover' | 'price' | 'latency' | 'success' | 'score' | 'random'
        >();
        expectTypeOf<GroupRelayConfig>().toHaveProperty('canary_item_id');
        expectTypeOf<GroupRelayConfig>().toHaveProperty('canary_percent');
        expectTypeOf<GroupRelayConfig>().toHaveProperty('canary_user_ids');
        expectTypeOf<GroupRelayConfig>().toHaveProperty('canary_per_request');
        expectTypeOf<GroupRelayConfig>().not.toHaveProperty('canary_fallback_mode');
        // 界面文案同理: 七个模式名仍在, 且没有新增的模式型灰度文案。
        for (const key of ['manual', 'failover', 'price', 'latency', 'success', 'score', 'random'] as const) {
            expect(en.group.form).toHaveProperty(key);
        }
        expect(Object.keys(en.group.form).filter((key) => key.includes('canaryFallback'))).toEqual([]);
        const html = renderEditor('failover');
        expect(html).not.toContain('canary_fallback_mode');
    });
});
