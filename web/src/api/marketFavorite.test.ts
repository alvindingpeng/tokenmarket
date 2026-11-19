import { describe, expect, it } from 'vitest';
import { favoriteRequestOf } from './marketFavorite';

// 锁的是「收藏方向」这个映射。它反过一次, 而且反了之后界面上一切看起来都正常:
// 已收藏时点击会再发一次 POST, 服务端幂等返回 200, 星标状态不变 —— 只有收藏数不动这一个线索,
// 用户只会觉得"点了没反应"。所以这里直接断言方向, 而不是去测组件。
describe('收藏请求方向', () => {
    it('已收藏时点击是取消(DELETE)', () => {
        expect(favoriteRequestOf(true)).toEqual({ method: 'DELETE', removing: true });
    });

    it('未收藏时点击是新增(POST)', () => {
        expect(favoriteRequestOf(false)).toEqual({ method: 'POST', removing: false });
    });

    it('两个方向必须相反: 任何一端写反都会让点击变成无效果', () => {
        // 这条是前两条的合取, 写它是因为真实的 bug 恰好是"两边都取反"——
        // 单独看任一条都只是"某个分支不对", 而它其实让整个交互空转。
        expect(favoriteRequestOf(true).removing).not.toBe(favoriteRequestOf(false).removing);
    });
});
