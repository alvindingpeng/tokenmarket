import { describe, expect, it } from 'vitest';
import { emptyFormState, fromChannel, toChannelConfig } from './state';
import type { ChannelDetail } from '@/api/channel';

// baseChannel 造一个最小可用渠道详情; 用例只覆盖需要改的字段。
function baseChannel(overrides: Partial<ChannelDetail> = {}): ChannelDetail {
    return {
        id: 1, name: 'ch', dialect: 'generic', enabled: true,
        base_url: 'https://up.example.com',
        openai_chat_completion_path: '/v1/chat/completions',
        openai_response_path: '/v1/responses',
        anthropic_message_path: '/v1/messages',
        keys: [], egresses: [], models: [], grants: [], proxy: false,
        custom_header: [], param_override: '', channel_proxy: '', match_regex: '',
        model_auto_add: false,
        ...overrides,
    } as ChannelDetail;
}

describe('订阅渠道的表单状态', () => {
    it('读回时把 sub_platform 带进状态', () => {
        const state = fromChannel(baseChannel({ dialect: 'subscription', sub_platform: 'gemini' }));
        expect(state.sub_platform).toBe('gemini');
    });

    it('历史渠道没有 sub_platform 时归一为空串', () => {
        // 归一很重要: 表单里处处少判一次 undefined。
        const state = fromChannel(baseChannel());
        expect(state.sub_platform).toBe('');
    });

    it('订阅渠道提交时带上平台名', () => {
        const config = toChannelConfig({ ...emptyFormState, dialect: 'subscription', sub_platform: 'gemini' });
        expect(config.sub_platform).toBe('gemini');
    });

    it('非订阅渠道提交空平台名, 不残留旧值', () => {
        // 后端对"非订阅方言带平台名"是硬报错。用户从订阅切回普通方言时,
        // 状态里的平台名若不发空串, 保存会被这条校验卡死且看不出原因。
        const config = toChannelConfig({ ...emptyFormState, dialect: 'generic', sub_platform: 'gemini' });
        expect(config.sub_platform).toBe('');
    });

    it('平台名两侧空白被去掉', () => {
        const config = toChannelConfig({ ...emptyFormState, dialect: 'subscription', sub_platform: '  gemini  ' });
        expect(config.sub_platform).toBe('gemini');
    });
});