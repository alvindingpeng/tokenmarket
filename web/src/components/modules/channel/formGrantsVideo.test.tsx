import { describe, expect, it } from 'vitest';
import { renderSSR } from '@/test/ssr';
import { TooltipProvider } from '@/components/ui/tooltip';
import { Protocol } from '@/api/channel';
import { FormGrants } from './FormGrants';
import { grantKey, emptyFormState, type ChannelFormState } from './state';
import en from '@/locales/en.json';

// FormGrants 的渲染用例: 视频协议列必须真的画出来。
//
// 修前的症状是『暗格』: 后端 internal/op/channel_probe.go 探测到输出视频的模型会自动预勾
// ProtocolOpenAIVideo 这一位(落库、能转发、能计费), 但授权矩阵只画 chat/response/message/image,
// 于是那条授权在界面上没有任何一列 —— 看不见, 也取消不了。
//
// 这里断言的是用户真正看得见的三处: 表头多了 video、复选框多了一格、该格处于已勾状态。
// SSR 无法点击, 所以『取消勾选后位被清掉』的断言放在 grantColumns.test.ts (纯函数) 与
// videoBasePath.test.ts (提交体) 里 —— 那两处断言的是同一个写路径。

const keys = [{ name: 'k1', key: 'sk', app_id: '', api_secret: '', domain: '', enabled: true }];

const grantsState = (overrides: Partial<ChannelFormState> = {}): ChannelFormState => ({
    ...emptyFormState,
    keys,
    models: ['veo-3'],
    ...overrides,
});

// FormGrants 里的删除按钮走 IconButton(带 Tooltip), 浏览器由 main.tsx 全局套 Provider, SSR 要自己给。
const render = (state: ChannelFormState) => renderSSR(
    <TooltipProvider><FormGrants state={state} setState={() => {}} /></TooltipProvider>,
);

const checkboxCount = (html: string) => html.split('role="checkbox"').length - 1;

describe('视频协议列渲染', () => {
    it('预勾了视频位的授权在界面上有一整列(表头写 video, 多一格已勾的复选框)', () => {
        // 表头 GrantCells(覆盖全部模型×凭据) + 模型行 GrantCells = 2 份, 每份一列一格。
        const html = render(grantsState({
            grants: new Map([[grantKey('veo-3', 'k1'), Protocol.OpenAIVideo]]),
        }));
        // 修前: 表头没有 video 这一项, 且复选框只有 4 列(共 8 个)。
        expect(html).toContain(
            [en.channel.form.grantProtocols, en.channel.form.grantProtocolVideo].join(' / '),
        );
        expect(checkboxCount(html)).toBe(2 * 5);
    });

    it('该列处于已勾状态(不是空列占位)', () => {
        // 若只画了列却不读状态, 用户会以为这条授权是空的, 于是又勾一遍(无害但白费),
        // 或者干脆以为视频没开 —— 后者才会真的去做错事。
        const html = render(grantsState({
            grants: new Map([[grantKey('veo-3', 'k1'), Protocol.OpenAIVideo]]),
        }));
        expect(html).toContain('aria-checked="true"');
    });

    it('generic 方言下即使一条授权都没有也显视频列(不依赖探测就能授予)', () => {
        // 这是『想给某模型开生视频、但探测没识别出来』的唯一入口。
        const html = render(grantsState({ grants: new Map() }));
        expect(html).toContain(` / ${en.channel.form.grantProtocolVideo}`);
        expect(checkboxCount(html)).toBe(2 * 5);
    });

    it('非 generic 方言带视频位的老数据同样显列, 且与 spark 列同时存在', () => {
        // 讯飞方言 + 视频位: 4 基线 + video + spark = 6 列。
        const html = render(grantsState({
            dialect: 'xunfei',
            grants: new Map([[grantKey('veo-3', 'k1'), Protocol.OpenAIVideo | Protocol.XunfeiSpark]]),
        }));
        // 表头是拼起来的单串: chat / response / message / image / video / spark
        expect(html).toContain(
            [en.channel.form.grantProtocols, en.channel.form.grantProtocolVideo, en.channel.form.grantProtocolSpark].join(' / '),
        );
        expect(checkboxCount(html)).toBe(2 * 6);
    });

    it('非 generic 方言且无视频位时不多这一列(表宽不白占)', () => {
        const html = render(grantsState({
            dialect: 'gemini_cli',
            grants: new Map([[grantKey('veo-3', 'k1'), Protocol.OpenAIChatCompletion]]),
        }));
        expect(html).not.toContain(en.channel.form.grantProtocolVideo);
        expect(checkboxCount(html)).toBe(2 * 4);
    });

    it('每一列都能在表头找到名字(列数与表头词数一致)', () => {
        // 少一个名字, 用户就只能按位置猜, 猜错会勾到隔壁协议(视频勾成图片不报错, 只会打错端点)。
        const html = render(grantsState({ grants: new Map([[grantKey('veo-3', 'k1'), Protocol.OpenAIVideo]]) }));
        const header = [en.channel.form.grantProtocols, en.channel.form.grantProtocolVideo]
            .join(' / ')
            .split(' / ');
        // 表头词数 = 每份 GrantCells 的协议格数(不含末尾的删除格占位)。
        expect(header.length).toBe(5);
        expect(checkboxCount(html) / 2).toBe(header.length);
    });
});
