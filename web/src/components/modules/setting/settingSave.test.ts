import { describe, expect, it } from 'vitest';
import { confirmedFromSettings, failedSave, rollbackValue, saveWithRollback } from './settingSave';

// 设置项保存失败的回滚基准。这一组钉住三件事:
//   1) 基准是"服务端确认过的值", 不来自查询缓存;
//   2) 连续两次编辑时, 回滚回到第一次成功的值, 而不是回到更早的初始值;
//   3) 没有基准时返回 undefined, 让调用方保持输入框不动(不制造第二个谎言)。

describe('confirmedFromSettings', () => {
    it('把设置列表转成 key→value 表', () => {
        expect(confirmedFromSettings([
            { key: 'proxy_url', value: 'http://a' },
            { key: 'model_filter', value: 'gpt-*' },
        ])).toEqual({ proxy_url: 'http://a', model_filter: 'gpt-*' });
    });

    it('列表还没到达时是空表(不是"所有键都等于空串")', () => {
        expect(confirmedFromSettings(undefined)).toEqual({});
    });

    it('空串是合法值, 不能被当成缺失丢掉', () => {
        // 管理员把某个设置清空是常见操作, 空串是真实已保存的状态。
        // 若把它当缺失, 回滚就会变成"不动输入框", 于是清空失败后界面仍显示空、服务端仍是旧值。
        expect(confirmedFromSettings([{ key: 'proxy_url', value: '' }])).toEqual({ proxy_url: '' });
    });
});

describe('rollbackValue', () => {
    it('有基准时返回基准值', () => {
        expect(rollbackValue({ proxy_url: 'http://old' }, 'proxy_url')).toBe('http://old');
    });

    it('基准是空串时返回空串, 而不是 undefined', () => {
        // 这两个在调用方是两条不同的分支: 空串 = "回滚到一个已知的空值", undefined = "没有基准, 别动"。
        expect(rollbackValue({ proxy_url: '' }, 'proxy_url')).toBe('');
    });

    it('没有基准时返回 undefined, 调用方据此保持输入框不动', () => {
        expect(rollbackValue({}, 'proxy_url')).toBeUndefined();
    });

    it('不误判继承来的键(toString 之类)', () => {
        // 用普通对象承载任意 key, 'toString' 这类名字会命中原型链上的属性。
        // 若用 in 运算符就会把原型方法当成值返回, 回滚时往输入框里塞一个函数源码。
        expect(rollbackValue({}, 'toString')).toBeUndefined();
        expect(rollbackValue({}, 'constructor')).toBeUndefined();
    });

    it('后续的确认值覆盖前一次的(时间序是准的)', () => {
        // 模拟: 初始 old -> 第一次编辑成功 a -> 第二次编辑失败。
        // 基准已更新为 a, 所以回滚到 a —— 回到 old 会让用户以为第一次改动也丢了。
        const confirmed: Record<string, string> = { proxy_url: 'http://old' };
        confirmed.proxy_url = 'http://a';
        expect(rollbackValue(confirmed, 'proxy_url')).toBe('http://a');
    });
});

describe('failedSave', () => {
    it('有基准时把输入框改回基准值, 并标记已回滚', () => {
        // 提示语「保存失败, 已还原」只有在真的还原了之后才能说。
        expect(failedSave({ proxy_url: 'http://old' }, 'proxy_url', 'http://new'))
            .toEqual({ input: 'http://old', reverted: true });
    });

    it('没有基准时保持输入框不变, 且不声称已回滚', () => {
        // 清空它既没回到服务端值(不知道是多少), 又抹掉用户刚敲的内容, 是第二重谎言。
        expect(failedSave({}, 'proxy_url', 'http://new'))
            .toEqual({ input: 'http://new', reverted: false });
    });

    it('基准是空串时回滚到空串(那是一个已确认的状态)', () => {
        // 与上一条对照: 空对象里的 key 是「从没成功保存过」, 值是空串是「成功保存过空值」。
        expect(failedSave({ proxy_url: '' }, 'proxy_url', 'http://new'))
            .toEqual({ input: '', reverted: true });
    });

    it('两个键互不干扰(一张表单里有十几个输入框)', () => {
        // 用错基准的后果是「改 A 失败, 结果 B 被改回去了」。
        expect(failedSave({ model_filter: 'gpt-*', proxy_url: 'http://old' }, 'proxy_url', 'http://new').input)
            .toBe('http://old');
    });
});

describe('saveWithRollback', () => {
    // 这一组是 D 那条缺陷的正面断言: 保存失败后输入框必须回到基准值。
    // 修前的症状是「界面停在用户敲的新值上, 服务端仍是旧值」—— 管理员按界面判断已经改了。

    it('成功时更新基准, 不动输入框', async () => {
        const confirmed: Record<string, string> = { site_name: 'old' };
        const applied: string[] = [];
        const result = await saveWithRollback({
            key: 'site_name', value: 'new', confirmed, currentInput: 'new',
            apply: (next) => applied.push(next),
            submit: async () => undefined,
        });
        expect(result.ok).toBe(true);
        // 基准只在服务端确认之后才更新 —— 这是回滚唯一可信的时间序。
        expect(confirmed.site_name).toBe('new');
        expect(applied).toEqual([]);
    });

    it('失败时把输入框改回基准值, 并报 reverted', async () => {
        const confirmed: Record<string, string> = { site_name: 'old' };
        const applied: string[] = [];
        const result = await saveWithRollback({
            key: 'site_name', value: 'new', confirmed, currentInput: 'new',
            apply: (next) => applied.push(next),
            submit: async () => { throw new Error('permission denied'); },
        });
        // 关键断言: apply 被调用, 且传入的是基准值 old —— 不是用户敲的 new, 也不是空串。
        expect(applied).toEqual(['old']);
        expect(result).toMatchObject({ ok: false, reverted: true, input: 'old', message: 'permission denied' });
    });

    it('失败且没有基准时保持输入框原样, 且不声称已还原', async () => {
        const applied: string[] = [];
        const result = await saveWithRollback({
            key: 'site_name', value: 'new', confirmed: {}, currentInput: 'new',
            apply: (next) => applied.push(next),
            submit: async () => { throw new Error('boom'); },
        });
        // 空串会同时抹掉用户刚敲的内容, 以及服务端到底是多少这条信息。
        expect(applied).toEqual(['new']);
        expect(result).toMatchObject({ ok: false, reverted: false, input: 'new' });
    });

    it('连续两次编辑: 第二次失败回到第一次成功的值, 不回到更早的初始值', async () => {
        // 这是「回滚基准取查询缓存」那种写法会错的地方: 缓存里还是最初的值,
        // 于是第二次失败会退到 old, 用户以为第一次改动也丢了。
        const confirmed: Record<string, string> = { site_name: 'old' };
        const applied: string[] = [];
        await saveWithRollback({ key: 'site_name', value: 'first', confirmed, currentInput: 'first',
            apply: (next) => applied.push(next), submit: async () => undefined });
        await saveWithRollback({ key: 'site_name', value: 'second', confirmed, currentInput: 'second',
            apply: (next) => applied.push(next), submit: async () => { throw new Error('boom'); } });
        expect(applied).toEqual(['first']);
    });

    it('失败的项不会污染基准(后续成功仍能正常推进)', async () => {
        const confirmed: Record<string, string> = { site_name: 'old' };
        const applied: string[] = [];
        await saveWithRollback({ key: 'site_name', value: 'bad', confirmed, currentInput: 'bad',
            apply: (next) => applied.push(next), submit: async () => { throw new Error('boom'); } });
        expect(confirmed.site_name).toBe('old');
        await saveWithRollback({ key: 'site_name', value: 'good', confirmed, currentInput: 'good',
            apply: (next) => applied.push(next), submit: async () => undefined });
        expect(confirmed.site_name).toBe('good');
    });
});
