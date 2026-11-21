import { Protocol } from '@/api/channel';
import type { ChannelFormState } from './state';

// 授权矩阵的「列」与「勾选」两件事的纯逻辑。
//
// 抽出来的原因: 这两件事此前只活在 FormGrants 的渲染里, 只能靠手点浏览器验证。
// 而它们各自守着一个不容易看出来的性质 ——「已存在的协议位不许被隐藏(暗格)」、
// 「取消勾选必须真的把位从 grants 里清掉」—— 这两条一旦回退, 界面看着都正常:
// 前者表现为一条授权在某方言下突然没有列可看, 后者表现为取消了、保存后仍然生效。

// 视频列约占的表宽(像素)。
//
// 列可见性是有取舍的: 表宽固定, 每加一列就挤窄模型名与凭据名。
// 所以只在 generic 方言下恒显(那里视频是开放的能力面), 其余方言按位出现。
export const VIDEO_COLUMN_COST = 28;

// hasBit 判断任意一条授权上是否已带该位。
export function hasBit(state: ChannelFormState, bit: number): boolean {
    for (const protocols of state.grants.values()) {
        if ((protocols & bit) !== 0) return true;
    }
    return false;
}

// grantColumns 返回授权矩阵要渲染的协议位列(固定顺序, 与表头一致)。
//
// 三条可见性规则, 每条都对应一类会被当成「暗格」的情形:
// 1. chat/response/message/image 恒显: 它们是基线能力, 没有列就没法授权。
// 2. spark 与 gemini 按方言展开, 但**已带该位的老数据恒显** —— 否则那条授权
//    在界面上无处查看、无处取消, 而它仍然在转发时生效。
// 3. 视频列同 2, 但额外在 generic 方言下恒显: 这是「不依赖上游探测就能授予」的唯一入口。
//    探测只在能识别出输出视频的模型上预勾这一位(见 internal/op/channel_probe.go);
//    若某上游的生视频模型探测认不出来, 只靠「位已存在」的判据就永远授予不了, 反过来也取消不了。
//    generic 是标准协议方言, 视频走的正是标准 /videos 路径, 在这里给列不增加误导。
//    代价是每条 generic 渠道的授权表都宽约 28px(一格复选框), 这是有意的取舍。
export function grantColumns(state: ChannelFormState): number[] {
    const columns = [Protocol.OpenAIChatCompletion, Protocol.OpenAIResponse, Protocol.AnthropicMessage, Protocol.OpenAIImage];
    if (state.dialect === 'generic' || hasBit(state, Protocol.OpenAIVideo)) columns.push(Protocol.OpenAIVideo);
    if (state.dialect === 'xunfei' || hasBit(state, Protocol.XunfeiSpark)) columns.push(Protocol.XunfeiSpark);
    if (state.dialect === 'subscription' || hasBit(state, Protocol.GeminiCLI)) columns.push(Protocol.GeminiCLI);
    return columns;
}

// 表头与列本体共用同一个判据(都问 grantColumns), 免得两处各写一份而漂开 ——
// 漂开的症状是"表头多写了一个词、下面却没有那一格", 看着像样式问题, 实际是判据分叉。
export function videoColumnVisible(state: ChannelFormState): boolean {
    return grantColumns(state).includes(Protocol.OpenAIVideo);
}
export function sparkColumnVisible(state: ChannelFormState): boolean {
    return grantColumns(state).includes(Protocol.XunfeiSpark);
}
export function geminiColumnVisible(state: ChannelFormState): boolean {
    return grantColumns(state).includes(Protocol.GeminiCLI);
}

// applyGrantToggle 按勾选控件**当前**的三态算出下一态并写回 grants, 返回新的 Map(不改入参)。
//
// 语义与界面一致: 当前已是全开(true) → 整片清掉该位; 全关(false)或半选(indeterminate) → 整片置位。
// 半选为什么算"往开的方向走": 用户看到的那一格不是勾上的, 点一下的期望是勾上,
// 若按"点一下就全关"处理, 会把一条他根本没打算取消的授权一起关掉。
//
// 一条授权的位被清空即该授权不存在, 直接从 Map 移除: 后端把「协议位为空的授权」当作删除,
// 留一条 0 值的条目既不会被提交(toChannelDetail 会过滤), 又会让「已授权」的计数多算一条。
export function applyGrantToggle(
    grants: Map<string, number>,
    models: string[],
    keyNames: string[],
    bit: number,
    current: boolean | 'indeterminate',
    grantKeyOf: (modelName: string, keyName: string) => string,
): Map<string, number> {
    const turnOn = current !== true;
    const next = new Map(grants);
    for (const modelName of models) {
        for (const keyName of keyNames) {
            const mapKey = grantKeyOf(modelName, keyName);
            const current = next.get(mapKey) ?? 0;
            const protocols = turnOn ? current | bit : current & ~bit;
            if (protocols === 0) next.delete(mapKey);
            else next.set(mapKey, protocols);
        }
    }
    return next;
}
