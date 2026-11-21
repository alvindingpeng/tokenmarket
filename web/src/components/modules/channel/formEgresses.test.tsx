import { describe, expect, it } from 'vitest';
import { renderSSR } from '@/test/ssr';
import { TooltipProvider } from '@/components/ui/tooltip';
import { FormEgresses } from './FormEgresses';
import { emptyFormState } from './state';
import type { ChannelFormState } from './state';
import en from '@/locales/en.json';
import zhHans from '@/locales/zh_hans.json';
import zhHant from '@/locales/zh_hant.json';

// 出口「权重」的口径曾经只在 placeholder 里写了两个字(「权重」/「Weight」),
// 而它的实际语义是**同分时的先后顺序**: 后端 internal/relay/egress.go 先比实测得分,
// 只有两条出口得分完全相同时才比 weight, 大的先上 —— 它不是流量比例。
//
// 这个误解在界面上无法自证: 使用者以为配了 80/20 分流, 实际流量全走一条, 而"看起来配好了"。
// 所以这组断言盯住两件事: 那行解释必须在, 且措辞必须点明"不是流量比例"。

// 出口行里的删除按钮走 IconButton, 带 Tooltip; 浏览器里由 main.tsx 全局套 Provider,
// SSR 测试得自己给 —— 缺了它直接抛 'Tooltip must be used within TooltipProvider'。
const egresses = (s: ChannelFormState) => (
    <TooltipProvider>
        <FormEgresses state={s} setState={() => {}} />
    </TooltipProvider>
);

const state: ChannelFormState = {
    ...emptyFormState,
    egresses: [
        { name: 'a', kind: 'direct', url: '', region: '', weight: 80, enabled: true },
        { name: 'b', kind: 'direct', url: '', region: '', weight: 20, enabled: true },
    ],
};

describe('FormEgresses 的权重的口径', () => {
    it('渲染出「同分优先」的解释', () => {
        const html = renderSSR(egresses(state));
        expect(html).toContain(en.channel.form.egressWeightHint);
    });

    it('权重的占位符用短标签, 语义交给那行解释', () => {
        const html = renderSSR(egresses(state));
        // 5rem 宽的格子里塞不下整句解释, 故 placeholder 只保留短标签。
        expect(html).toContain(`placeholder="${en.channel.form.egressWeight}"`);
        // 读屏软件读的是 aria-label, 与可见的占位符同一个词, 免得同一格有两个说法。
        expect(html).toContain(`aria-label="${en.channel.form.egressWeight}"`);
    });

    it('中文界面同样给出解释, 且点明不是流量比例', () => {
        const html = renderSSR(egresses(state), undefined, {
            locale: 'zh_hans',
            messages: zhHans,
        });
        expect(html).toContain(zhHans.channel.form.egressWeightHint);
        // 「比例」这个词是这行文案存在的理由: 只有把它写出来, 才挡住了"80 就是八成分流"的误读。
        expect(zhHans.channel.form.egressWeightHint).toContain('比例');
    });

    it('空出口池仍渲染空态', () => {
        const html = renderSSR(egresses(emptyFormState));
        expect(html).toContain(en.channel.form.egressEmpty);
    });

    it('权重值按行渲染出来(不会被 0 兜底逻辑改写)', () => {
        const html = renderSSR(egresses(state));
        expect(html).toContain('value="80"');
        expect(html).toContain('value="20"');
    });
});

describe('轮换策略在界面上的入口', () => {
    // 选择器在 Form.tsx 的 advanced 步骤里, 拿整张表单做 SSR 成本高(要 mock 详情查询),
    // 故这里只钉住"三语文案是否到位": 缺键时界面会印出原始 key, 而这一栏是新加的,
    // 最容易漏掉其中某一语。
    const LOCALES = [
        ['en', en],
        ['zh_hans', zhHans],
        ['zh_hant', zhHant],
    ] as const;

    for (const [name, dict] of LOCALES) {
        it(`${name} 的轮换策略文案齐全`, () => {
            const form = dict.channel.form;
            expect(form.keyRotation).toBeTruthy();
            expect(form.keyRotationHint).toBeTruthy();
            expect(form.keyRotationRoundrobin).toBeTruthy();
            expect(form.keyRotationRandom).toBeTruthy();
            expect(form.keyRotationFailover).toBeTruthy();
        });
    }

    it('三种策略的标签互不相同(否则选择器选了也看不出来)', () => {
        const labels = [
            en.channel.form.keyRotationRoundrobin,
            en.channel.form.keyRotationRandom,
            en.channel.form.keyRotationFailover,
        ];
        expect(new Set(labels).size).toBe(3);
    });
});
