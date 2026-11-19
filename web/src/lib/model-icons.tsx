import type { ComponentType } from 'react';
import type { SvgIconProps } from '@thesvg/react';
import OpenAIIcon from '@thesvg/react/openai';
import ClaudeIcon from '@thesvg/react/claude';
import GeminiIcon from '@thesvg/react/gemini';
import DeepSeekIcon from '@thesvg/react/deepseek';
import MistralIcon from '@thesvg/react/mistral';
import QwenIcon from '@thesvg/react/qwen';
import MetaIcon from '@thesvg/react/meta';
import CohereIcon from '@thesvg/react/cohere';
import PerplexityIcon from '@thesvg/react/perplexity';
import ZhipuIcon from '@thesvg/react/zhipu';
import YiIcon from '@thesvg/react/yi';
import KimiIcon from '@thesvg/react/kimi';
import MinimaxIcon from '@thesvg/react/minimax';
import DoubaoIcon from '@thesvg/react/doubao';
import HunyuanIcon from '@thesvg/react/hunyuan';
import SparkIcon from '@thesvg/react/spark';
import WenxinIcon from '@thesvg/react/wenxin';
import NvidiaIcon from '@thesvg/react/nvidia-nemotron';
import GrokIcon from '@thesvg/react/grok-xai';
import InternLMIcon from '@thesvg/react/internlm';
import StepfunIcon from '@thesvg/react/stepfun';
import MicrosoftIcon from '@thesvg/react/microsoft';
import KwaiKATIcon from '@thesvg/react/kwaikat-kat-coder';
import XiaomiMimoIcon from '@thesvg/react/xiaomi-mimo';
import AmazonIcon from '@thesvg/react/amazon';
import VercelIcon from '@thesvg/react/vercel';
import BlackForestIcon from '@thesvg/react/black-forest-labs-bfl';
import RunwayIcon from '@thesvg/react/runway';
import RecraftIcon from '@thesvg/react/recraft';
import IdeogramIcon from '@thesvg/react/ideogram';
import ElevenLabsIcon from '@thesvg/react/elevenlabs';

type IconConfig = {
    Icon: ComponentType<SvgIconProps>;
    className?: string;
    color: string;
};

// 品牌键 → 图标映射。品牌键由后端 op.MarketBrandOf 解析(见 internal/op/market_brand.go),
// 键集合与 internal/model/market.go 的 MarketBrand* 常量一一对应。
// 前端不再自带一套关键词表: 两处各带一份会让同一个模型出现"后端说 openai、前端画 deepseek 图标"的分叉,
// 且广场的筛选结果与图标不一致时无从判断哪边对。
const BRAND_ICONS: Record<string, IconConfig> = {
    openai: { Icon: OpenAIIcon, className: 'brightness-0 dark:invert', color: '#10A37F' },
    anthropic: { Icon: ClaudeIcon, color: '#D7765A' },
    google: { Icon: GeminiIcon, color: '#4285F4' },
    deepseek: { Icon: DeepSeekIcon, color: '#4D6BFE' },
    xai: { Icon: GrokIcon, color: '#000000' },
    alibaba: { Icon: QwenIcon, className: 'brightness-0 dark:invert', color: '#6B4EFF' },
    zhipu: { Icon: ZhipuIcon, color: '#3C5BFC' },
    minimax: { Icon: MinimaxIcon, color: '#1A1A2E' },
    moonshot: { Icon: KimiIcon, color: '#000000' },
    mistral: { Icon: MistralIcon, color: '#F7D046' },
    meta: { Icon: MetaIcon, color: '#0668E1' },
    xiaomi: { Icon: XiaomiMimoIcon, color: '#FF6900' },
    bytedance: { Icon: DoubaoIcon, color: '#00D6C2' },
    tencent: { Icon: HunyuanIcon, color: '#0052D9' },
    iflytek: { Icon: SparkIcon, color: '#0078FF' },
    baidu: { Icon: WenxinIcon, color: '#2932E1' },
    stepfun: { Icon: StepfunIcon, color: '#5B5CFF' },
    nvidia: { Icon: NvidiaIcon, color: '#76B900' },
    cohere: { Icon: CohereIcon, color: '#39594D' },
    perplexity: { Icon: PerplexityIcon, color: '#20B8CD' },
    microsoft: { Icon: MicrosoftIcon, color: '#00BCF2' },
    kuaishou: { Icon: KwaiKATIcon, color: '#1969FC' },
    yi: { Icon: YiIcon, color: '#1B1464' },
    internlm: { Icon: InternLMIcon, color: '#2F54EB' },
    amazon: { Icon: AmazonIcon, color: '#FF9900' },
    vercel: { Icon: VercelIcon, color: '#000000' },
    blackforest: { Icon: BlackForestIcon, color: '#000000' },
    runway: { Icon: RunwayIcon, color: '#000000' },
    recraft: { Icon: RecraftIcon, color: '#000000' },
    ideogram: { Icon: IdeogramIcon, color: '#000000' },
    elevenlabs: { Icon: ElevenLabsIcon, color: '#000000' },
};

// FALLBACK_ICON 是品牌解析不出时的默认图标: 与既有默认一致(OpenAI 徽标 + 经典绿),
// 保持历史观感不变, 同时不硬造一个"其他厂商"的假图标。
const FALLBACK_ICON: IconConfig = { Icon: OpenAIIcon, className: 'brightness-0 dark:invert', color: '#10A37F' };

/**
 * getBrandIcon 按品牌键取图标。广场一律走它(品牌来自后端 op.MarketBrandOf)。
 * 键未登记时回落默认图标, 不抛错 —— 后端新增品牌时前端不该白屏。
 */
export function getBrandIcon(brand: string): IconConfig {
    return BRAND_ICONS[brand] ?? FALLBACK_ICON;
}

// MODEL_NAME_KEYWORDS 是按模型名猜品牌的本地兜底, 只服务于"手上只有模型名"的调用方
// (模型价格库、日志、模型配置成员列表)。广场不要用它: 那里有后端算好的 brand 字段。
// 顺序即优先级(先命中先返回), 故前缀类关键词放在前面。
const MODEL_NAME_KEYWORDS: { brand: string; keywords: string[] }[] = [
    { brand: 'openai', keywords: ['gpt-', 'o1-', 'o3-', 'o4-', 'chatgpt', 'text-embedding', 'dall-e', 'sora', 'openai'] },
    { brand: 'anthropic', keywords: ['claude', 'anthropic'] },
    { brand: 'google', keywords: ['gemini', 'gemma', 'palm', 'imagen', 'veo-'] },
    { brand: 'xiaomi', keywords: ['xiaomi', 'mimo'] },
    { brand: 'deepseek', keywords: ['deepseek'] },
    { brand: 'xai', keywords: ['grok', 'xai'] },
    { brand: 'alibaba', keywords: ['qwen', 'qwq', 'alibaba', 'tongyi'] },
    { brand: 'zhipu', keywords: ['glm', 'chatglm', 'zhipu', 'z-ai'] },
    { brand: 'minimax', keywords: ['minimax', 'abab'] },
    { brand: 'moonshot', keywords: ['moonshot', 'kimi'] },
    { brand: 'mistral', keywords: ['mistral', 'mixtral', 'codestral', 'pixtral'] },
    { brand: 'meta', keywords: ['llama', 'meta-llama'] },
    { brand: 'bytedance', keywords: ['doubao', 'skylark', 'bytedance', 'seedance', 'seedream'] },
    { brand: 'yi', keywords: ['yi-', '01-ai'] },
    { brand: 'tencent', keywords: ['hunyuan'] },
    { brand: 'iflytek', keywords: ['spark'] },
    { brand: 'baidu', keywords: ['ernie', 'wenxin'] },
    { brand: 'internlm', keywords: ['internlm'] },
    { brand: 'stepfun', keywords: ['stepfun', 'step-'] },
    { brand: 'nvidia', keywords: ['nvidia', 'nemotron'] },
    { brand: 'cohere', keywords: ['cohere', 'command'] },
    { brand: 'perplexity', keywords: ['perplexity'] },
    { brand: 'microsoft', keywords: ['phi-'] },
    { brand: 'amazon', keywords: ['amazon', 'bedrock', 'titan'] },
    { brand: 'blackforest', keywords: ['flux', 'black-forest'] },
    { brand: 'runway', keywords: ['runway', 'gen-3', 'gen-4'] },
    { brand: 'recraft', keywords: ['recraft'] },
    { brand: 'ideogram', keywords: ['ideogram'] },
    { brand: 'elevenlabs', keywords: ['elevenlabs', 'eleven-'] },
    { brand: 'kuaishou', keywords: ['kling', 'kwaikat'] },
];

/** getModelBrand 按模型名猜本地品牌键; 猜不出返回空串(调用方回落默认图标)。 */
export function getModelBrand(modelName: string): string {
    const lowerName = modelName.toLowerCase();
    for (const { brand, keywords } of MODEL_NAME_KEYWORDS) {
        if (keywords.some((keyword) => lowerName.includes(keyword))) return brand;
    }
    return '';
}

/**
 * getModelIcon 按模型名取图标。
 *
 * 保持既有签名: 六个调用点(模型价格库、模型配置编辑器/成员列表、日志明细)都只有模型名,
 * 改签名会让它们全都得跟着改。这里内部改成"先猜品牌键再查同一张表",
 * 于是图标口径只有一处(BRAND_ICONS), 广场与这些页面不会出现同模型不同图标。
 */
export function getModelIcon(modelName: string): IconConfig {
    const brand = getModelBrand(modelName);
    return brand ? getBrandIcon(brand) : FALLBACK_ICON;
}
