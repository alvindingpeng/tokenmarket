import { describe, expect, it } from 'vitest';
import type { ChannelReviewStatus } from '@/api/channel';
import { reviewBadgeKey, reviewBadgeTone } from './reviewBadge';

// 回归 v0.39.6。
//
// 渠道列表曾经漏拷 review_status, 前端收到的状态是空串, 徽标随即渲染成裸 key
// "channel.review.status."。裸 key 上屏既是数据泄露也是纯视觉事故。
//
// 本项目单测跑在 node 环境(没有 jsdom), 渲染 React 组件不可行, 所以这里直接钉住徽标取 key
// 与取配色这两步 —— 裸 key 的源头就在这两步: 状态非法时必须回落, 不能拼出带点号的路径型 key,
// 也不能取到 undefined 的配色类。

const ALL: ChannelReviewStatus[] = ['draft', 'pending', 'approved', 'rejected', 'stale'];

describe('reviewBadgeKey 回归 v0.39.6', () => {
    it.each(ALL)('%s 拼出合法译文 key', (status) => {
        expect(reviewBadgeKey(status)).toBe('status.' + status);
    });

    // 空串是 v0.39.6 之前的实际输入: 后端的 review_status 恒为空。
    it('空状态回落为待审核, 不拼出裸 key', () => {
        expect(reviewBadgeKey('' as ChannelReviewStatus)).toBe('status.pending');
    });

    it('未知状态与 undefined 同样回落, 不再原样透传', () => {
        expect(reviewBadgeKey('bogus' as ChannelReviewStatus)).toBe('status.pending');
        expect(reviewBadgeKey(undefined as unknown as ChannelReviewStatus)).toBe('status.pending');
    });

    it('任何输入都不会产生以点号结尾的 key', () => {
        const inputs: ChannelReviewStatus[] = ['', ...ALL, 'unknown', undefined] as ChannelReviewStatus[];
        for (const input of inputs) {
            expect(reviewBadgeKey(input).endsWith('.')).toBe(false);
        }
    });
});

describe('reviewBadgeTone 与 key 同步回落', () => {
    it.each(ALL)('%s 取到非空配色类', (status) => {
        expect(reviewBadgeTone(status)).toBeTruthy();
    });

    it('空状态与未知状态都回落到待审核的配色', () => {
        expect(reviewBadgeTone('' as ChannelReviewStatus)).toBe(reviewBadgeTone('pending'));
        expect(reviewBadgeTone('bogus' as ChannelReviewStatus)).toBe(reviewBadgeTone('pending'));
    });
});
