import type { ChannelReviewStatus } from '@/api/channel';

// statusTone 五个审核态的徽章配色: draft 静默, pending 琥珀, approved 绿, rejected 红, stale 橙。
const statusTone: Record<ChannelReviewStatus, string> = {
    draft: 'bg-muted text-muted-foreground',
    pending: 'bg-amber-500/15 text-amber-600 dark:text-amber-400',
    approved: 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400',
    rejected: 'bg-destructive/15 text-destructive',
    stale: 'bg-orange-500/15 text-orange-600 dark:text-orange-400',
};

// reviewBadgeKey 徽标取译文用的 key, 非法的状态一律回落待审核。
//
// 不回落就会拼出 "status." 这种以点号结尾的 key, t() 找不到便原样返回 —— 徽标直接上屏裸 key。
// 这条兜底并非防御性摆设: v0.39.6 之前后端列表漏拷 review_status, 前端收到的就是空串,
// 五张卡片全都渲染成 "channel.review.status."。颜色的回落同理, 否则 statusTone[undefined] 也是 undefined。
//
// 单独成文件: Review.tsx 只导出组件, 混入常量与函数会触发 react-refresh/only-export-components。
export function reviewBadgeKey(status: ChannelReviewStatus): string {
    return status in statusTone ? `status.${status}` : 'status.pending';
}

// reviewBadgeTone 徽标配色, 非法状态与 reviewBadgeKey 同步回落。
export function reviewBadgeTone(status: ChannelReviewStatus): string {
    return statusTone[status] ?? statusTone.pending;
}
