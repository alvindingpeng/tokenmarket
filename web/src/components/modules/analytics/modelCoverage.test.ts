import { describe, expect, it } from 'vitest';
import { modelCoverageOf } from './modelCoverage';

// 按模型拆分的覆盖边界判定。
// 这五种输入直接对应后端契约的每一句话, 也是界面上"什么会显示、什么绝不该显示"的全部依据:
// 缺失(老后端)不许编提示、空串是"完全没有"、晚于区间起点才是"前段只有总额"、等于/早于起点都是完整。

const RANGE_START = '2026-01-01';

describe('modelCoverageOf: 字段缺失不猜', () => {
    it('overview 未就绪(undefined)时什么都不做', () => {
        // 请求还没回来 / 失败了: 没有事实依据, 不该先摆一条覆盖提示。
        expect(modelCoverageOf(undefined, RANGE_START)).toEqual({ kind: 'full' });
        expect(modelCoverageOf(null, RANGE_START)).toEqual({ kind: 'full' });
    });

    it('老后端没有这个字段时不硬报缺失', () => {
        // 伪造一条"覆盖不全"与漏报一样是错: 用户会以为数据一定缺了一截。
        expect(modelCoverageOf({}, RANGE_START)).toEqual({ kind: 'full' });
        expect(modelCoverageOf({ model_breakdown_coverage_start: undefined }, RANGE_START)).toEqual({ kind: 'full' });
        expect(modelCoverageOf({ model_breakdown_coverage_start: null }, RANGE_START)).toEqual({ kind: 'full' });
        expect(modelCoverageOf({ model_breakdown_coverage_start: 20260101 }, RANGE_START)).toEqual({ kind: 'full' });
    });

    it('拿不到查询起点时不下"是否早于区间"的结论', () => {
        // 没有区间起点就无从比较; 猜一个只会把提示变成噪声。
        expect(modelCoverageOf({ model_breakdown_coverage_start: '2026-01-05' }, undefined)).toEqual({ kind: 'full' });
    });
});

describe('modelCoverageOf: 空串 = 区间内按模型完全没有数据', () => {
    it('空串走 none, 与"有日期"严格区分', () => {
        // 两种空值语义不同: 空串是"没有", 有日期是"有但看不见"。混成一种就没法如实表达。
        expect(modelCoverageOf({ model_breakdown_coverage_start: '' }, RANGE_START)).toEqual({ kind: 'none' });
    });

    it('纯空白同样当作空串', () => {
        expect(modelCoverageOf({ model_breakdown_coverage_start: '   ' }, RANGE_START)).toEqual({ kind: 'none' });
    });
});

describe('modelCoverageOf: 有日期才有 from', () => {
    it('晚于查询起点 = 前段金额有拆分无', () => {
        expect(modelCoverageOf({ model_breakdown_coverage_start: '2026-01-11' }, RANGE_START))
            .toEqual({ kind: 'from', start: '2026-01-11' });
    });

    it('等于查询起点 = 整段都有拆分, 不多嘴', () => {
        // 严格晚于才算缺口: 第一天就有拆分, 没有可讲的话。
        expect(modelCoverageOf({ model_breakdown_coverage_start: RANGE_START }, RANGE_START)).toEqual({ kind: 'full' });
    });

    it('早于查询起点(区间本就在留存期内)= 整段完整', () => {
        expect(modelCoverageOf({ model_breakdown_coverage_start: '2025-12-20' }, RANGE_START)).toEqual({ kind: 'full' });
    });

    it('边界日期按字符串序比较, 不解析成 Date', () => {
        // YYYY-MM-DD 定长零填充, 字符串序即日期序; 跨月/跨年也要判对。
        expect(modelCoverageOf({ model_breakdown_coverage_start: '2026-02-01' }, '2026-01-31'))
            .toEqual({ kind: 'from', start: '2026-02-01' });
        // 跨年: 覆盖起点在上一年, 查询起点在 12-31 —— 起点早于区间开始, 整段都完整。
        expect(modelCoverageOf({ model_breakdown_coverage_start: '2025-12-01' }, '2025-12-31'))
            .toEqual({ kind: 'full' });
    });

    it('两端空白被修掉后再比较, 不因为空格判成缺口', () => {
        // 脏数据不该凭空造出一条提示。
        expect(modelCoverageOf({ model_breakdown_coverage_start: ' 2026-01-01 ' }, RANGE_START)).toEqual({ kind: 'full' });
    });
});
