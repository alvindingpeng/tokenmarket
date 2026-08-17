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
import GoogleIcon from '@thesvg/react/google';
import InternLMIcon from '@thesvg/react/internlm';
import StepfunIcon from '@thesvg/react/stepfun';
import GemmaIcon from '@thesvg/react/gemma-google';
import MicrosoftIcon from '@thesvg/react/microsoft';
import KwaiKATIcon from '@thesvg/react/kwaikat-kat-coder';

type ModelIconConfig = {
    prefixes: string[]; // 用于匹配模型名称的前缀。
    Icon: ComponentType<SvgIconProps>; // 模型研发商的 React SVG 组件。
    className?: string; // 仅用于修正默认白色图标在不同主题下的可见性。
    color: string; // 页面徽标使用的品牌色。
};

/**
 * 模型研发商配置，包含模型名前缀、图标组件和品牌色。
 */
const MODEL_ICON_PATTERNS: ModelIconConfig[] = [
    // OpenAI - GPT series
    { prefixes: ['gpt-', 'o1', 'o3', 'o4', 'chatgpt', 'text-embedding', 'dall-e', 'openai'], Icon: OpenAIIcon, className: 'brightness-0 dark:invert', color: '#10A37F' },
    // Anthropic - Claude series
    { prefixes: ['claude', 'anthropic'], Icon: ClaudeIcon, color: '#D7765A' },
    // Google - Gemini series
    { prefixes: ['gemini'], Icon: GeminiIcon, color: '#4285F4' },
    { prefixes: ['gemma'], Icon: GemmaIcon, color: '#4285F4' },
    { prefixes: ['palm', 'google'], Icon: GoogleIcon, color: '#4285F4' },
    // DeepSeek series
    { prefixes: ['deepseek'], Icon: DeepSeekIcon, color: '#4D6BFE' },
    // xAI - Grok series
    { prefixes: ['grok', 'xai'], Icon: GrokIcon, color: '#000000' },
    // Alibaba - Qwen series
    { prefixes: ['qwen', 'qwq', 'alibaba'], Icon: QwenIcon, className: 'brightness-0 dark:invert', color: '#6B4EFF' },
    // Zhipu - GLM series
    { prefixes: ['glm', 'chatglm', 'zhipu', 'z-ai'], Icon: ZhipuIcon, color: '#3C5BFC' },
    // MiniMax series
    { prefixes: ['minimax', 'abab'], Icon: MinimaxIcon, color: '#1A1A2E' },
    // Moonshot/Kimi series
    { prefixes: ['moonshot', 'kimi'], Icon: KimiIcon, color: '#000000' },
    // Mistral series
    { prefixes: ['mistral', 'mixtral', 'codestral', 'pixtral'], Icon: MistralIcon, color: '#F7D046' },
    // Meta - Llama series
    { prefixes: ['llama', 'meta-llama', 'meta'], Icon: MetaIcon, color: '#0668E1' },
    // ByteDance - Doubao series
    { prefixes: ['doubao', 'skylark', 'bytedance'], Icon: DoubaoIcon, color: '#00D6C2' },
    // Yi series
    { prefixes: ['yi-', '01-ai'], Icon: YiIcon, color: '#1B1464' },
    // Tencent - Hunyuan
    { prefixes: ['hunyuan'], Icon: HunyuanIcon, color: '#0052D9' },
    // iFlytek - Spark
    { prefixes: ['spark'], Icon: SparkIcon, color: '#0078FF' },
    // Baidu - ERNIE/Wenxin
    { prefixes: ['ernie', 'wenxin', 'baidu'], Icon: WenxinIcon, color: '#2932E1' },
    // InternLM
    { prefixes: ['internlm'], Icon: InternLMIcon, color: '#2F54EB' },
    // Stepfun
    { prefixes: ['stepfun', 'step-'], Icon: StepfunIcon, color: '#5B5CFF' },
    // NVIDIA - Nemotron series
    { prefixes: ['nvidia', 'nemotron'], Icon: NvidiaIcon, color: '#76B900' },
    // 其他模型研发商
    { prefixes: ['cohere', 'command'], Icon: CohereIcon, color: '#39594D' },
    { prefixes: ['perplexity'], Icon: PerplexityIcon, color: '#20B8CD' },
    { prefixes: ['phi-'], Icon: MicrosoftIcon, color: '#00BCF2' },
    { prefixes: ['kat'], Icon: KwaiKATIcon, color: '#1969FC' },
];

// 未匹配模型时使用的默认配置。
const DEFAULT_CONFIG = { Icon: OpenAIIcon, className: 'brightness-0 dark:invert', color: '#10A37F' };

/**
 * 获取模型图标数据和品牌色。
 * @param modelName - The name of the model
 * @returns React SVG 组件、可选样式和品牌色。
 */
export function getModelIcon(modelName: string): { Icon: ComponentType<SvgIconProps>; className?: string; color: string } {
    // Extract the part after the first '/' if it exists
    // e.g., "qwen/gpt-5.2" -> "gpt-5.2"
    const nameToMatch = modelName.includes('/') ? modelName.split('/')[1] : modelName;
    const lowerName = nameToMatch.toLowerCase();
    for (const { prefixes, Icon, className, color } of MODEL_ICON_PATTERNS) {
        if (prefixes.some(prefix => lowerName.startsWith(prefix))) {
            return { Icon, className, color };
        }
    }
    return DEFAULT_CONFIG;
}
