/**
 * 文本模型单价的输入解析与前端校验。
 *
 * 背景: 计费价走的是「每百万 token」的单价, LLMPrice.Cost 是纯乘法再求和,
 * 没有下界钳制 —— 负价会算出负费用, 即"调用反而加钱"; 非数字经 parseFloat(x) || 0
 * 会静默变成 0, 也就是把付费模型悄悄改成免费。两种错误在界面上都看不出来,
 * 所以要在这里显式拦住, 并且与后端 model.LLMPrice.Validate 保持同一口径。
 */

/** 与后端 model.MaxLLMPricePerMillion 对应的量级上限。 */
export const MAX_LLM_PRICE_PER_MILLION = 1_000_000;

export type PriceParseResult =
    | { ok: true; value: number }
    | { ok: false; reason: 'not_a_number' | 'negative' | 'too_large' };

/**
 * 解析一个单价输入框的值。
 *
 * 空串视为 0 —— 免费模型是合法配置, 留空不该报错。
 * 其余情况必须是一个有限的非负数, 且不超过量级上限。
 */
export function parsePriceInput(raw: string | undefined | null): PriceParseResult {
    const text = (raw ?? '').trim();
    if (text === '') return { ok: true, value: 0 };
    // Number 而不是 parseFloat: parseFloat('1.2.3') 会得到 1.2 并静默丢弃后半段,
    // 那正是"看起来填错了却存进去了"的来源。
    const value = Number(text);
    if (!Number.isFinite(value)) return { ok: false, reason: 'not_a_number' };
    if (value < 0) return { ok: false, reason: 'negative' };
    if (value > MAX_LLM_PRICE_PER_MILLION) return { ok: false, reason: 'too_large' };
    return { ok: true, value };
}

/**
 * 批量解析四个单价字段; 任一项失败即返回第一个失败原因。
 *
 * 一次性校验四项而不是逐项提交: 四个框是同一个价格表, 提交时才可能发现问题,
 * 而部分成功会让价格表处于半新半旧的状态。
 */
export type LLMPriceFormValues = {
    input: string;
    output: string;
    cache_read: string;
    cache_write: string;
};

export type LLMPriceFields = {
    input: number;
    output: number;
    cache_read: number;
    cache_write: number;
};

export function parseLLMPriceForm(form: LLMPriceFormValues):
    | { ok: true; fields: LLMPriceFields }
    | { ok: false; reason: 'not_a_number' | 'negative' | 'too_large' } {
    const out: LLMPriceFields = { input: 0, output: 0, cache_read: 0, cache_write: 0 };
    for (const key of ['input', 'output', 'cache_read', 'cache_write'] as const) {
        const parsed = parsePriceInput(form[key]);
        if (!parsed.ok) return { ok: false, reason: parsed.reason };
        out[key] = parsed.value;
    }
    return { ok: true, fields: out };
}
