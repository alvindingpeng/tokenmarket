// formatPlainMoney 把金额渲染成最多 6 位小数并去掉尾部 0, 不带货币符号。
// 与 lib/utils 的 formatMoney(带单位/量级的展示型)不同: 这里要的是可读可复制的原始数字。
// 步骤顺序是刻意的: 先 toFixed(6) 定精度, 再 \.?0+$ 去尾零; 单独用 replace(/\.?0+$/,"") 会把整数 100 也吃成 1。
export function formatPlainMoney(value: number): string {
  return value.toFixed(6).replace(/\.?0+$/, '') || '0';
}
