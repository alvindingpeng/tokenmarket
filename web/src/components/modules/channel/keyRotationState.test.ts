import { describe, expect, it } from 'vitest';
import { emptyFormState, fromChannel, toChannelConfig, toChannelDetail } from './state';
import type { ChannelDetail, KeyRotationStrategy } from '@/api/channel';

// 这一组守的是 key_rotation_strategy 在表单里的三个边界:
// 读回来是"实际生效的值", 新建时有个明确的默认值, 提交时恒带值。
//
// 为什么值得单测: 这一列在落库层是 NOT NULL DEFAULT 'roundrobin'。
// 若 fromChannel 在缺省时不给值, 表单里那一栏会是空的, 保存时按空串提交 ——
// 后端会把现有策略覆盖成空串, 下一轮 SelectChannelKey 落进 default 分支转圈,
// 症状是"改过策略的渠道自己变回轮询", 而且没有任何报错。

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

describe('key_rotation_strategy 的表单状态', () => {
    it('读回时带出已有的策略', () => {
        const state = fromChannel(baseChannel({ key_rotation_strategy: 'failover' }));
        expect(state.key_rotation_strategy).toBe('failover');
    });

    // 历史渠道这一列可能缺省(读侧 DTO 是可选的): 与后端列默认值保持同一个值,
    // 这样界面上看到的就是"接下来会实际生效的那个", 不是空。
    it('缺省时回落到 roundrobin, 与后端列默认值一致', () => {
        expect(fromChannel(baseChannel()).key_rotation_strategy).toBe('roundrobin');
    });

    it('新建渠道默认 roundrobin', () => {
        expect(emptyFormState.key_rotation_strategy).toBe('roundrobin');
    });

    it('三种策略都能原样读回并提交', () => {
        const strategies: KeyRotationStrategy[] = ['roundrobin', 'random', 'failover'];
        for (const strategy of strategies) {
            const state = fromChannel(baseChannel({ key_rotation_strategy: strategy }));
            expect(state.key_rotation_strategy).toBe(strategy);
            expect(toChannelConfig(state).key_rotation_strategy).toBe(strategy);
        }
    });

    // 必须恒带值: 缺省提交会被后端当作空串, 把现有策略冲掉。
    it('提交时恒带值, 不会因为表单未设置而变空', () => {
        expect(toChannelConfig(emptyFormState).key_rotation_strategy).toBe('roundrobin');
        expect(toChannelDetail(emptyFormState, 0).key_rotation_strategy).toBe('roundrobin');
    });

    it('创建与更新提交的是同一份值(create 用 id=0, update 用已有 id)', () => {
        const state = { ...emptyFormState, key_rotation_strategy: 'random' as KeyRotationStrategy };
        expect(toChannelDetail(state, 0).key_rotation_strategy).toBe('random');
        expect(toChannelDetail(state, 7).key_rotation_strategy).toBe('random');
    });
});
