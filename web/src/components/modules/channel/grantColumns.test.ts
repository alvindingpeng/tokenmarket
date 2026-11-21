import { describe, expect, it } from 'vitest';
import { Protocol } from '@/api/channel';
import { applyGrantToggle, grantColumns, hasBit, sparkColumnVisible, videoColumnVisible } from './grantColumns';
import { grantKey, type ChannelFormState, emptyFormState } from './state';

// 授权矩阵「列」与「勾选写入」的纯逻辑用例。
//
// 这两条性质在界面上都看不出来, 一旦回退就成了暗格:
// - 列: 已存在的位必须有列可看可改, 且必须存在一条不依赖上游探测的授予入口;
// - 勾选: 取消必须真的把位清掉(否则界面显示取消、保存后仍生效)。

const stateWith = (overrides: Partial<ChannelFormState> = {}): ChannelFormState => ({
    ...emptyFormState,
    keys: [{ name: 'k1', key: 'sk', app_id: '', api_secret: '', domain: '', enabled: true }],
    ...overrides,
});

const BASE_COLUMNS = [Protocol.OpenAIChatCompletion, Protocol.OpenAIResponse, Protocol.AnthropicMessage, Protocol.OpenAIImage];

describe('视频协议列(Protocol.OpenAIVideo = 1 << 5)', () => {
    it('常量位值与后端 model.ProtocolOpenAIVideo 一致', () => {
        // 位值错一位不会报错, 只会让界面勾上的那一格对应到别的协议 —— 只有断言具体数值能挡住。
        expect(Protocol.OpenAIVideo).toBe(1 << 5);
        expect(Protocol.OpenAIVideo).toBe(32);
    });

    it('generic 方言恒显视频列: 这是不依赖上游探测就能授予视频位的唯一入口', () => {
        // 探测只在识别出输出视频的模型上预勾这一位; 认不出来的模型如果也没有列,
        // 那条生视频授权就永远授予不了 —— 界面上没有任何入口。
        const state = stateWith({ dialect: 'generic', models: ['m1'], grants: new Map() });
        expect(videoColumnVisible(state)).toBe(true);
        expect(grantColumns(state)).toContain(Protocol.OpenAIVideo);
    });

    it('已带视频位的老数据在**任何**方言下都显列(不隐藏已存在的位)', () => {
        // 这是本用例独立存在的理由: 列可见性可以取舍, 但『位已存在却没有列』
        // 就是暗格 —— 那条授权仍在转发时生效, 管理员却无处查看、无处取消。
        for (const dialect of ['generic', 'xunfei', 'gemini_cli', 'subscription'] as const) {
            const state = stateWith({ dialect, grants: new Map([[grantKey('veo-3', 'k1'), Protocol.OpenAIVideo]]) });
            expect({ dialect, visible: videoColumnVisible(state) }).toEqual({ dialect, visible: true });
        }
    });

    it('非 generic 方言且无视频位时不显列(表宽是有限资源)', () => {
        const state = stateWith({ dialect: 'xunfei', grants: new Map([[grantKey('m1', 'k1'), Protocol.OpenAIChatCompletion]]) });
        expect(videoColumnVisible(state)).toBe(false);
        // 其余基线列不受影响。
        expect(grantColumns(state).slice(0, 4)).toEqual(BASE_COLUMNS);
    });

    it('列顺序固定: 基线四列在前, 视频在 spark / gemini 之前', () => {
        // 顺序漂了会让表头文案与复选框逐一错位 —— 用户照着表头点, 勾到的是隔壁协议。
        const state = stateWith({
            dialect: 'xunfei',
            grants: new Map([
                [grantKey('m1', 'k1'), Protocol.OpenAIVideo | Protocol.XunfeiSpark],
                [grantKey('m2', 'k1'), Protocol.GeminiCLI],
            ]),
        });
        expect(grantColumns(state)).toEqual([
            Protocol.OpenAIChatCompletion, Protocol.OpenAIResponse, Protocol.AnthropicMessage, Protocol.OpenAIImage,
            Protocol.OpenAIVideo, Protocol.XunfeiSpark, Protocol.GeminiCLI,
        ]);
    });

    it('hasBit 只看任意一条授权上是否带位', () => {
        expect(hasBit(stateWith({ grants: new Map() }), Protocol.OpenAIVideo)).toBe(false);
        expect(hasBit(stateWith({ grants: new Map([[grantKey('m', 'k'), Protocol.OpenAIVideo]]) }), Protocol.OpenAIVideo)).toBe(true);
    });
});

describe('取消勾选必须真的清掉协议位', () => {
    it('全开时点一下 → 该位从 Map 消失(不是留一条 0 值)', () => {
        // 留一条 0 值: toChannelDetail 会过滤掉, 界面上却仍算一条『已有授权』, 计数与徽章因此说谎。
        const grants = new Map([[grantKey('veo-3', 'k1'), Protocol.OpenAIVideo]]);
        const next = applyGrantToggle(grants, ['veo-3'], ['k1'], Protocol.OpenAIVideo, true, grantKey);
        expect(next.has(grantKey('veo-3', 'k1'))).toBe(false);
    });

    it('取消视频位后其余位保留', () => {
        // 只清目标位: 连带清掉 chat/response 会把一条能对话的授权弄哑。
        const grants = new Map([[grantKey('m1', 'k1'), Protocol.OpenAIVideo | Protocol.OpenAIResponse]]);
        const next = applyGrantToggle(grants, ['m1'], ['k1'], Protocol.OpenAIVideo, true, grantKey);
        expect(next.get(grantKey('m1', 'k1'))).toBe(Protocol.OpenAIResponse);
    });

    it('未勾时点一下 → 置上该位', () => {
        const next = applyGrantToggle(new Map(), ['m1'], ['k1'], Protocol.OpenAIVideo, false, grantKey);
        expect(next.get(grantKey('m1', 'k1'))).toBe(Protocol.OpenAIVideo);
    });

    it('半选时点一下 → 全开, 不是全关', () => {
        // 半选在用户眼里不是『已勾上』, 点一下的期望是勾上;
        // 若当全关处理, 会把他没打算取消的那一半也关掉。
        const grants = new Map([
            [grantKey('m1', 'k1'), Protocol.OpenAIVideo],
            [grantKey('m2', 'k1'), Protocol.OpenAIResponse],
        ]);
        const next = applyGrantToggle(grants, ['m1', 'm2'], ['k1'], Protocol.OpenAIVideo, 'indeterminate', grantKey);
        expect(next.get(grantKey('m1', 'k1'))).toBe(Protocol.OpenAIVideo);
        expect(next.get(grantKey('m2', 'k1'))).toBe(Protocol.OpenAIVideo | Protocol.OpenAIResponse);
    });

    it('批量: 跨模型跨凭据同时清位', () => {
        const grants = new Map([
            [grantKey('m1', 'k1'), Protocol.OpenAIVideo],
            [grantKey('m1', 'k2'), Protocol.OpenAIVideo],
        ]);
        const next = applyGrantToggle(grants, ['m1'], ['k1', 'k2'], Protocol.OpenAIVideo, true, grantKey);
        expect(next.size).toBe(0);
    });

    it('不改入参 Map(调用方拿旧 state 比较时才不会互相污染)', () => {
        const grants = new Map([[grantKey('m1', 'k1'), Protocol.OpenAIVideo]]);
        applyGrantToggle(grants, ['m1'], ['k1'], Protocol.OpenAIVideo, true, grantKey);
        expect(grants.get(grantKey('m1', 'k1'))).toBe(Protocol.OpenAIVideo);
    });
});

describe('spark / gemini 列沿用原有判据', () => {
    it('讯飞方言恒显 spark 列; 其它方言仅在带位时显', () => {
        expect(sparkColumnVisible(stateWith({ dialect: 'xunfei' }))).toBe(true);
        expect(sparkColumnVisible(stateWith({ dialect: 'generic' }))).toBe(false);
        expect(sparkColumnVisible(stateWith({ dialect: 'generic', grants: new Map([[grantKey('m', 'k'), Protocol.XunfeiSpark]]) }))).toBe(true);
    });

    it('订阅方言恒显 gemini 列; 其它方言仅在带位时显', () => {
        expect(grantColumns(stateWith({ dialect: 'subscription' })).includes(Protocol.GeminiCLI)).toBe(true);
        expect(grantColumns(stateWith({ dialect: 'generic' })).includes(Protocol.GeminiCLI)).toBe(false);
    });
});
