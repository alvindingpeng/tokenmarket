// SHELL_HEIGHT 渠道弹窗的高度阶梯: 常态取固定 rem(与统计/编辑切换同高不缩放),
// 但必须按可视区高度夹住, 否则矮窗口/手机上底部按钮行会被弹窗裁掉。
// 单独成文件: 表单与统计卡都要用, 放在组件文件里会破坏 Fast Refresh 的组件唯一导出约束。
export const SHELL_HEIGHT = 'h-[min(24rem,calc(100dvh_-_5rem))] md:h-[min(29rem,calc(100dvh_-_5rem))]';
