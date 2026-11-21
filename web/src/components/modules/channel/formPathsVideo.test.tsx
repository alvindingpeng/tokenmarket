import { describe, expect, it } from 'vitest';
import { renderSSR } from '@/test/ssr';
import { FormPaths } from './FormPaths';
import { emptyFormState, type ChannelFormState } from './state';
import en from '@/locales/en.json';

// FormPaths 的渲染用例: 视频基路径必须有输入框, 且框里就是实际会提交的那个值。
//
// 修前的症状: Form.tsx 的路径组只有 chat/response/anthropic 三项,
// 于是 openai_video_base_path 对界面完全不可见 —— 想从 /v1 改成 /v2 只能直接打 API。
//
// 为什么单独测这个组件而不测整张表单: ChannelFormFields 要挂在 MorphingDialog 的 Provider 上,
// 而弹窗正文在 SSR 里渲染不出来(容器要等一次 setTimeout(0) 的挂载 tick, 且只在 isOpen 时渲染)。
// 抽成 FormPaths 之后, "输入框里有没有那个值"就能被直接断言。

const formOf = (overrides: Partial<ChannelFormState> = {}): ChannelFormState => ({ ...emptyFormState, ...overrides });

const render = (state: ChannelFormState) => renderSSR(
    <FormPaths state={state} setState={() => {}} idPrefix="channel-7" />,
);

describe('视频基路径输入框', () => {
    it('渲染出 OpenAI Video 这一项, 且值就是 state 里的值', () => {
        const html = render(formOf({ openai_video_base_path: '/v2' }));
        expect(html).toContain('OpenAI Video');
        expect(html).toContain('id="channel-7-openai_video_base_path"');
        expect(html).toContain('value="/v2"');
        expect(html).not.toContain('aria-invalid="true"');
    });

    it('默认值是 /v1(与后端列默认值一致, 不是空框)', () => {
        const html = render(emptyFormState);
        expect(html).toContain('value="/v1"');
    });

    it('不以斜杠开头时标 aria-invalid(与相邻三项同一套校验)', () => {
        const html = render(formOf({ openai_video_base_path: 'v2' }));
        expect(html).toContain('aria-invalid="true"');
    });

    it('空串不标非法(它是"视频挂域名根下"的合法配置)', () => {
        const html = render(formOf({ openai_video_base_path: '' }));
        expect(html).not.toContain('aria-invalid="true"');
    });

    it('带一句说明"填的是基路径, /videos 由后端拼", 防止填成完整端点', () => {
        const html = render(formOf({ openai_video_base_path: '/v1' }));
        expect(html).toContain(en.channel.form.openaiVideoBasePathHint);
    });

    it('四个输入框各自独立, 不共用同一个 id', () => {
        const html = render(emptyFormState);
        for (const field of ['openai_chat_completion_path', 'openai_response_path', 'anthropic_message_path', 'openai_video_base_path']) {
            expect(html).toContain('id="channel-7-' + field + '"');
        }
    });
});
