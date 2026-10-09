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
