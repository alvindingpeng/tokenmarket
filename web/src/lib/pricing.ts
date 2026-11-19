import { SettingKey, type Setting } from '@/api/setting';

// markupOf 读取全局上浮比例, 用于把供货价换算成用户实际支付单价。
// 非法值(缺设置项、非数字、负数)一律回落 0: 展示层宁可少算也不能显示 NaN ——
// 上浮是可选项, 缺省即"不加价", 不是错误状态。
export function markupOf(settings: Setting[] | undefined): number {
  const raw = (settings ?? []).find((item) => item.key === SettingKey.MarkupRatio)?.value;
  const value = Number(raw);
  return Number.isFinite(value) && value > 0 ? value : 0;
}

// userPriceOf 按上浮比例把供货价换算为用户价, 与后端计费口径一致(用户价 = 供货价 × (1 + 上浮))。
export function userPriceOf(supply: number, markup: number): number {
  return supply * (1 + markup);
}

// FxRateItem 最小形状: 与 api/fx.ts 的 FxRateItem 对齐, 这里只关心币种和牌价。
type FxRateLite = { to: string; rate: number };

// ledgerRateOf 原币 → 账面 USD 的折算系数(1 原币折多少 USD)。
// rates 口径与后端一致: 1 USD 折多少 quote, 所以系数是 1/rate。
// USD 或空币种恒为 1; 缺牌价返回 undefined —— 调用方必须显式处理, 不能回落 1(那等于把原币当 USD)。
export function ledgerRateOf(rates: FxRateLite[] | undefined, currency?: string): number | undefined {
  const c = (currency ?? '').trim().toUpperCase();
  if (!c || c === 'USD') return 1;
  const rate = (rates ?? []).find((item) => item.to === c)?.rate;
  return rate && rate > 0 ? 1 / rate : undefined;
}

// FX_STALE_DAYS 牌价可接受的最大日龄, 与后端 FxRateMaxAgeDays(2 天)一致。
// 采集是小时级, 快照超过 2 天说明连续采集失败, 折算误差已不可忽略。
export const FX_STALE_DAYS = 2;

// fxRateAgeDays 快照日龄(天)。day 形如 YYYYMMDD; 非法格式返回 0, 由调用方用其它口径兜底。
export function fxRateAgeDays(day: string, now: number = Date.now()): number {
  const ms = Date.parse(day.replace(/(\d{4})(\d{2})(\d{2})/, '$1-$2-$3T00:00:00'));
  return Number.isNaN(ms) ? 0 : Math.floor((now - ms) / 86400000);
}

// fxRateIsStale 牌价是否陈旧: 超过 FX_STALE_DAYS 天、解析失败(非法格式, 同样视为异常)、
// 或解析出负日龄(未来日期), 都按陈旧处理 —— 宁可多警告也不能漏。
export function fxRateIsStale(day: string, now: number = Date.now()): boolean {
  if (!/^\d{8}$/.test(day)) return true;
  const age = fxRateAgeDays(day, now);
  return age < 0 || age > FX_STALE_DAYS;
}

// userPriceInLedger 原币供货价 → 账面 USD 用户价(先折算再上浮, 与后端结算同一口径)。
// 缺牌价返回 undefined, 由调用方决定如何呈现, 绝不静默按 USD 显示。
export function userPriceInLedger(supply: number, currency: string | undefined, rates: FxRateLite[] | undefined, markup: number): number | undefined {
  const rate = ledgerRateOf(rates, currency);
  return rate === undefined ? undefined : supply * rate * (1 + markup);
}
