import type { ChannelBreakerResetRequest, ChannelStatsFormatted } from '@/api/channel';

// channelPauseResetVariables 是"恢复整条渠道"的提交形状。
// 渠道级额度暂停与模型级熔断共用一个解除接口, 差别只在 channel 标志位:
// 抽成独立模块是为了让界面与测试对齐同一份形状, 也避免组件文件导出非组件而妨碍热更新。
export function channelPauseResetVariables(channel: ChannelStatsFormatted): ChannelBreakerResetRequest {
    return { id: channel.channel_id, channel: true };
}
