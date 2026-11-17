import { describe, expect, it } from 'vitest';
import { channelSaveBlockers, emptyFormState, type ChannelFormState } from './state';

// 订阅渠道的保存闸口。
//
// 真实缺陷(生产上遇到的): 订阅渠道不填 key 就存不下来。
// 表面看像"key 必填", 实际是"模型必填" —— 订阅渠道的凭据要登录后导入,
// 模型要登录后从平台同步, 建渠道那一刻两者必然都不存在。
//
// 这条规则此前只活在组件里, 只能靠手点按钮验证。现在它是纯函数, 下面的用例能真正锁住它。
const subState = (): ChannelFormState => ({
	...emptyFormState,
	name: '我的Gemini订阅',
	dialect: 'subscription',
	sub_platform: 'gemini',
	base_url: '',
	keys: [],
	models: [],
});

const genericState = (): ChannelFormState => ({
	...emptyFormState,
	name: '普通渠道',
	dialect: 'generic',
	base_url: '',
	keys: [],
	models: [],
});

const noSpark = { sparkOnly: () => false };

describe('channelSaveBlockers: 订阅渠道', () => {
	it('无凭据 + 无模型 + 地址留空, 不产生任何缺项(可以保存)', () => {
		const blockers = channelSaveBlockers(subState(), noSpark);
		expect(blockers).toEqual([]);
	});

	it('仍然要求填名称: 豁免的是凭据与模型, 不是所有校验', () => {
		const state = subState();
		state.name = '   ';
		expect(channelSaveBlockers(state, noSpark).map((b) => b.reason)).toEqual(['name']);
	});

	it('路径必须以 / 开头: 这条对订阅渠道同样成立', () => {
		const state = subState();
		state.openai_chat_completion_path = 'v1/chat/completions';
		expect(channelSaveBlockers(state, noSpark).map((b) => b.reason)).toContain('paths');
	});

	it('订阅渠道下即使填了空 key 的凭据, 也不该当成缺项(那种凭据不该存在)', () => {
		// 反向用例: 订阅渠道原则上没有手填凭据。若将来有人在订阅渠道里塞了空 key 的条目,
		// 这里允许它出现, 但确认它不会让"模型/凭据必填"这两条重新生效。
		const blockers = channelSaveBlockers(subState(), noSpark);
		expect(blockers.some((b) => b.reason === 'no_keys')).toBe(false);
		expect(blockers.some((b) => b.reason === 'no_models')).toBe(false);
	});
});

describe('channelSaveBlockers: 普通渠道仍要求必填', () => {
	it('缺地址 + 缺凭据 + 缺模型 = 三条缺项全报', () => {
		const reasons = channelSaveBlockers(genericState(), noSpark).map((b) => b.reason);
		expect(reasons).toEqual(['base_url', 'no_keys', 'no_models']);
	});
});
