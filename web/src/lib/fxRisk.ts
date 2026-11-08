import type { FxRateItem } from '@/api/fx';

// FxRiskLevel 非账面币种报价的汇率风险分级(渠道商改价工作台用):
//   usd     = 报价即账面币种, 无折算风险;
//   ok      = 有牌价且够新, 按此牌价折算的 USD 等值可信;
//   stale   = 牌价偏旧(超过 staleDays 天), 结算时的汇率可能与现在看到的相差明显;
//   missing = 无牌价, 汇率就绪前该报价无法折算, 结算侧按缺价处理(渠道不参与计费)。
export type FxRiskLevel = 'usd' | 'ok' | 'stale' | 'missing';

export type FxRisk = {
  level: FxRiskLevel;
  /** 牌价采集日 YYYYMMDD; missing 时为空串。 */
  day: string;
  /** 牌价日龄(天)。 */
  ageDays: number;
};

// FX_RISK_DEFAULT_STALE_DAYS 可接受牌价日龄缺省值, 与后端 op.FxRateMaxAgeDays 一致。
export const FX_RISK_DEFAULT_STALE_DAYS = 2;

// fxRiskOf 评估某报价币种的汇率风险。
// rates 为 undefined(查询未返回)与空数组一样按 missing 处理: 对渠道商来说
// 「还没拿到牌价」和「确实没有牌价」都是不能安心提交原币报价的同一类风险。
export function fxRiskOf(
  rates: FxRateItem[] | undefined,
  currency: string | undefined,
  staleDays: number = FX_RISK_DEFAULT_STALE_DAYS
): FxRisk {
  const code = (currency ?? '').trim().toUpperCase();
  if (code === '' || code === 'USD') return { level: 'usd', day: '', ageDays: 0 };
  const item = (rates ?? []).find((entry) => entry.to === code);
  if (!item || !(item.rate > 0) || !item.day) {
    return { level: 'missing', day: '', ageDays: 0 };
  }
  // YYYYMMDD 按本地时区解析成日历日, 与后端 day 字段语义一致; 与今天差整日数。
  const parsed = new Date(
    Number(item.day.slice(0, 4)),
    Number(item.day.slice(4, 6)) - 1,
    Number(item.day.slice(6, 8))
  );
  const today = new Date();
  const todayMidnight = new Date(today.getFullYear(), today.getMonth(), today.getDate());
  const ageDays = Number.isNaN(parsed.getTime())
    ? staleDays + 1 // 无法解析的采集日按超龄处理, 保守提示而不是装作新鲜。
    : Math.max(0, Math.round((todayMidnight.getTime() - parsed.getTime()) / 86_400_000));
  return ageDays > staleDays
    ? { level: 'stale', day: item.day, ageDays }
    : { level: 'ok', day: item.day, ageDays };
}

// fxRiskBlocksSave 提交闸口: 汇率风险是否应阻止保存该行价格。
// 只拦 missing —— 无牌价时折算不存在, 价格语义不完整;
// stale 只是警告(旧牌价仍是真实结算依据), 交给渠道商自行判断。
export function fxRiskBlocksSave(risk: FxRisk | undefined): boolean {
  return risk?.level === 'missing';
}
