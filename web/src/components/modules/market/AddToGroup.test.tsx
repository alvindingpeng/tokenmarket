import { describe, expect, it } from 'vitest';
import type { Group } from '@/api/group';
import { addPreview } from './AddToGroup';

// 提交预览的纯函数测试。
//
// 为什么不走 SSR 断言面板内部: 加入面板挂在弹层里, 弹层关闭时面板内容根本不渲染,
// 而"点提交前会加几条、跳过几条"恰恰是用户唯一的确认依据 —— 这段逻辑必须被测试直接钉住。
// 把计算抽成导出的纯函数, 就既能测到, 又不必去模拟开合动画。

const group = (id: number, name: string, grantIDs: number[]): Group => ({
    id,
    name,
    mode: 'failover',
    relay_config: {} as Group['relay_config'],
    items: grantIDs.map((grantID, index) => ({
        id: index + 1, group_id: id, channel_grant_id: grantID, priority: index + 1,
        channel_id: 100, channel_name: '#1001', model_name: 'm', key_name: '',
        protocols: 1, available: true,
        supply_price: { input: 0, output: 0, cache_read: 0, cache_write: 0 },
        user_price: { input: 0, output: 0, cache_read: 0, cache_write: 0 },
    })),
    runtime: { group_id: id, current_item_id: 0, probe_item_id: 0, affinity_until: 0, cooldowns: {} } as Group['runtime'],
});

describe('addPreview 提交预览', () => {
    it('算出每个分组各会新增几条', () => {
        const rows = addPreview([group(1, 'a', []), group(2, 'b', [])], [1, 2], [11, 12]);
        expect(rows.map((row) => row.toAdd.length)).toEqual([2, 2]);
        expect(rows.every((row) => row.skipped === 0)).toBe(true);
    });

    it('已存在的渠道计入跳过, 不承诺新增', () => {
        // g1 已有 11, 提交 11+12 => 新增 1 条、跳过 1 条。
        const rows = addPreview([group(1, 'a', [11])], [1], [11, 12]);
        expect(rows[0].toAdd).toEqual([12]);
        expect(rows[0].skipped).toBe(1);
    });

    it('请求内重复的授权只算一次, 与后端去重口径一致', () => {
        // 后端对重复授权只加一次; 预览若按请求原样计数, 会承诺"加 2 条"而实际只加 1 条。
        const rows = addPreview([group(1, 'a', [])], [1], [11, 11]);
        expect(rows[0].toAdd).toEqual([11]);
        expect(rows[0].skipped).toBe(0);
    });

    it('全部已存在时新增为零, 界面据此提示"无需新增"而不是失败', () => {
        const rows = addPreview([group(1, 'a', [11, 12])], [1], [11, 12]);
        expect(rows[0].toAdd).toEqual([]);
        expect(rows[0].skipped).toBe(2);
    });

    it('忽略不存在的分组主键, 不产出幽灵行', () => {
        const rows = addPreview([group(1, 'a', [])], [1, 999], [11]);
        expect(rows).toHaveLength(1);
        expect(rows[0].group.id).toBe(1);
    });
});
