import { describe, expect, it } from 'vitest';
import { Protocol, type ChannelDetail } from '@/api/channel';
import { channelSaveBlockers, emptyFormState, fromChannel, grantKey, toChannelConfig, toChannelDetail, type ChannelFormState } from './state';

// 视频基路径(openai_video_base_path)的往返与提交体用例。
//
// 修前的症状: 这个字段在 ChannelFormState 里根本不存在, 于是
// - 读回来的 /v2 被丢弃, 界面显示不了;
// - 提交体里也没有它, 想改只能直接打 API;
// - 更糟的是后端列 NOT NULL 且保存时归一, 少发这一项等于把它改回默认 /v1(静默改动线上转发路径)。
//
// 三条断言分别落在三个用户看得见的地方: 输入框里有值、提交体里有值、非法值挡住保存。

function baseChannel(overrides: Partial<ChannelDetail> = {}): ChannelDetail {
    return {
        id: 7, name: 'ch', dialect: 'generic', enabled: true,
        base_url: 'https://up.example.com',
        openai_chat_completion_path: '/v1/chat/completions',
        openai_response_path: '/v1/responses',
        anthropic_message_path: '/v1/messages',
        keys: [{ name: 'k1', key: 'sk', enabled: true }],
        egresses: [], models: ['veo-3'],
        grants: [{ model_name: 'veo-3', key_name: 'k1', protocols: Protocol.OpenAIVideo }],
        proxy: false, custom_header: [], param_override: '', channel_proxy: '', match_regex: '',
        model_auto_add: false,
        ...overrides,
    } as ChannelDetail;
}

const formOf = (overrides: Partial<ChannelFormState> = {}): ChannelFormState => ({ ...emptyFormState, ...overrides });

describe('fromChannel: 读侧归一', () => {
    it('后端下发的 /v2 原样带进表单', () => {
        const state = fromChannel(baseChannel({ openai_video_base_path: '/v2' }));
        expect(state.openai_video_base_path).toBe('/v2');
    });

    it('历史渠道缺这一列时归一为 /v1(与后端列默认值一致)', () => {
        // 缺省与空串必须分开处理: 缺省是"老库没有这一列", 空串是"视频挂域名根下"的既有配置。
        const state = fromChannel(baseChannel());
        expect(state.openai_video_base_path).toBe('/v1');
    });

    it('后端下发的空串保留为空串, 不归一成 /v1', () => {
        // 这条是"静默改动线上转发路径"的防线:
        // 一条配成根路径(/ 经后端 TrimSuffix 后落成空串)的渠道, 若前端把它归一成 /v1,
        // 用户下次点保存就会把所有视频请求悄悄挪到 /v1/videos。
        const state = fromChannel(baseChannel({ openai_video_base_path: '' }));
        expect(state.openai_video_base_path).toBe('');
    });
});

describe('toChannelConfig: 写侧恒带值', () => {
    it('空串提交成斜杠(经后端 TrimSuffix 落回空串, 原值不丢)', () => {
        // 后端 normalizedPath 把空串当"未填"填成 /v1; 提交 / 才能让它归一成空串。
        const config = toChannelConfig(formOf({ openai_video_base_path: '' }));
        expect(config.openai_video_base_path).toBe('/');
    });

    it('/v2 原样提交, 不被改写成别的值', () => {
        const config = toChannelConfig(formOf({ openai_video_base_path: '/v2' }));
        expect(config.openai_video_base_path).toBe('/v2');
    });

    it('两侧空白被去掉', () => {
        const config = toChannelConfig(formOf({ openai_video_base_path: '  /v3  ' }));
        expect(config.openai_video_base_path).toBe('/v3');
    });

    it('前端不做 TrimSuffix: 结尾斜杠原样提交, 归一留给后端', () => {
        // 两边都归一迟早会漂(后端还要处理只填 / 的情况); 前端只负责如实提交。
        const config = toChannelConfig(formOf({ openai_video_base_path: '/v2/' }));
        expect(config.openai_video_base_path).toBe('/v2/');
    });
});

describe('fromChannel -> toChannelConfig 往返', () => {
    it('非空值往返不丢', () => {
        const state = fromChannel(baseChannel({ openai_video_base_path: '/v2' }));
        expect(toChannelConfig(state).openai_video_base_path).toBe('/v2');
    });

    it('缺省值往返后仍是默认语义的 /v1', () => {
        const state = fromChannel(baseChannel());
        expect(toChannelConfig(state).openai_video_base_path).toBe('/v1');
    });

    it('空串往返后仍是空串的语义(提交斜杠, 后端归一为空)', () => {
        const state = fromChannel(baseChannel({ openai_video_base_path: '' }));
        expect(toChannelConfig(state).openai_video_base_path).toBe('/');
    });
});

describe('提交体里真的带上了视频位与视频基路径', () => {
    it('toChannelDetail 的提交体带 1<<5 与 /v2', () => {
        // 这是用户看得见的那一处: 点保存时发出去的就是这一份。
        // 只断言 state 的话, toChannelDetail 里漏映射也照样绿。
        const state = fromChannel(baseChannel({ openai_video_base_path: '/v2' }));
        const detail = toChannelDetail(state, 7);
        expect(detail.openai_video_base_path).toBe('/v2');
        expect(detail.grants).toContainEqual(
            { model_name: 'veo-3', key_name: 'k1', protocols: Protocol.OpenAIVideo },
        );
        expect(detail.grants.some((g) => (g.protocols & Protocol.OpenAIVideo) !== 0)).toBe(true);
    });

    it('取消视频位后提交体里不再含 1<<5', () => {
        // 取消勾选若只改了本地展示, 这条会红 —— 也就是"界面显示取消、保存后仍生效"。
        const state = fromChannel(baseChannel());
        const grants = new Map(state.grants);
        grants.set(grantKey('veo-3', 'k1'), Protocol.OpenAIResponse);
        const detail = toChannelDetail({ ...state, grants }, 7);
        expect(detail.grants.some((g) => (g.protocols & Protocol.OpenAIVideo) !== 0)).toBe(false);
        expect(detail.grants).toContainEqual(
            { model_name: 'veo-3', key_name: 'k1', protocols: Protocol.OpenAIResponse },
        );
    });

    it('视频位全被取消时该条授权整体从提交体消失(不留 0 值条目)', () => {
        const state = fromChannel(baseChannel());
        const detail = toChannelDetail({ ...state, grants: new Map() }, 7);
        expect(detail.grants).toEqual([]);
    });
});

describe('保存闸门认得视频基路径', () => {
    it('不以斜杠开头时挡住保存, 原因是 paths', () => {
        const blockers = channelSaveBlockers(formOf({
            name: 'ch', base_url: 'https://up.example.com',
            keys: [{ name: 'k1', key: 'sk', app_id: '', api_secret: '', domain: '', enabled: true }],
            models: ['veo-3'],
            openai_video_base_path: 'v2',
        }), { sparkOnly: () => false });
        expect(blockers.map((b) => b.reason)).toContain('paths');
    });

    it('空串不挡保存(它是"视频挂域名根下"的合法配置)', () => {
        // 把空串当"未填"拦下来, 那条渠道一旦被重新保存就再也存不回去。
        const blockers = channelSaveBlockers(formOf({
            name: 'ch', base_url: 'https://up.example.com',
            keys: [{ name: 'k1', key: 'sk', app_id: '', api_secret: '', domain: '', enabled: true }],
            models: ['veo-3'],
            openai_video_base_path: '',
        }), { sparkOnly: () => false });
        expect(blockers.map((b) => b.reason)).not.toContain('paths');
    });
});
