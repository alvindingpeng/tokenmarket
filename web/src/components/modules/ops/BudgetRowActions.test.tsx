import { describe, expect, it } from 'vitest';
import { renderSSR } from '@/test/ssr';
import zhHans from '@/locales/zh_hans.json';
import { BudgetRowActions } from './BudgetRowActions';

// 预算表格操作区的二次确认(mock SSR)。
//
// 后端 internal/server/handlers/budget.go 删预算没有 step-up, 所以前端这次确认是唯一一道闸。
//
// 关于这套测试能证明什么、不能证明什么, 先讲清楚免得被误读:
//   · 不能证明: 真点两下之后的网络行为 —— 那是 jsdom/e2e 的领域, 本仓库单测跑在 node 里没有 DOM。
//   · 能证明: 第一下点击之后界面必须进入确认态, 而确认态是**唯一**渲染破坏性按钮的分支。
//     也就是说, 只要确认态没被渲染出来, 那个会发请求的 onClick 在 DOM 里根本不存在 ——
//     一次点击在结构上不可能删掉预算。这条比「点一下看看有没有真删」更根本。

const props = {
    rowId: 7,
    deleting: false,
    toggling: false,
    onAsk: () => {},
    onCancel: () => {},
    onDelete: () => {},
    onToggle: () => {},
};

// buttonCount 数渲染出几颗按钮: 用来确认没有顺手多给一个能点的东西。
function buttonCount(html: string): number {
    return html.split('<button').length - 1;
}

describe('BudgetRowActions 空闲态', () => {
    it('只渲染两颗进入确认态的按钮, 不渲染确认提示', () => {
        const html = renderSSR(<BudgetRowActions {...props} enabled pending={null} />);
        expect(buttonCount(html)).toBe(2);
        // 关键断言: 空闲态没有确认提示。有提示就说明确认分支被渲染了, 也就说明一次点击即可删。
        expect(html).not.toContain('Delete this budget?');
        expect(html).not.toContain('Disable this budget?');
    });

    it('停用中的预算在空闲态显示的是启用', () => {
        const html = renderSSR(<BudgetRowActions {...props} enabled={false} pending={null} />);
        expect(html).toContain('Enable');
    });
});

describe('BudgetRowActions 确认态', () => {
    it('删除确认态: 出现确认提示, 仍是两颗按钮(确认与取消)', () => {
        const html = renderSSR(<BudgetRowActions {...props} enabled pending={{ id: 7, kind: 'delete' }} />);
        expect(html).toContain('Delete this budget?');
        expect(buttonCount(html)).toBe(2);
    });

    it('停用确认态: 提示说清停用之后不再拦截', () => {
        const html = renderSSR(<BudgetRowActions {...props} enabled pending={{ id: 7, kind: 'toggle' }} />);
        expect(html).toContain('Disable this budget?');
        expect(html).toContain('Yes, disable');
    });

    it('启用确认态: 与停用是两套文案, 不共用', () => {
        const html = renderSSR(<BudgetRowActions {...props} enabled={false} pending={{ id: 7, kind: 'toggle' }} />);
        expect(html).toContain('Enable this budget again?');
        expect(html).toContain('Yes, enable');
        expect(html).not.toContain('Yes, disable');
    });

    it('确认态精确到行: 别的行在确认态时本行仍是空闲态', () => {
        // 用布尔而不是「行 id + 种类」的话, 点第 2 行会让第 1 行也亮起确认态 ——
        // 那时候按下去删掉的是第 1 条预算, 而用户以为在操作第 2 条。
        const html = renderSSR(<BudgetRowActions {...props} rowId={1} enabled pending={{ id: 2, kind: 'delete' }} />);
        expect(html).not.toContain('Delete this budget?');
        expect(html).toContain('Delete');
    });

    it('中文环境下确认态接上了中文文案, 不是 i18n 路径串', () => {
        // en 里 confirmDelete 与 delete 都是 Delete, 看不出区别; 中文里是「确认删除」与「删除」,
        // 这正是区分「进入确认」与「真的执行」的那个界面差别。
        const html = renderSSR(<BudgetRowActions {...props} enabled pending={{ id: 7, kind: 'delete' }} />,
            undefined, { locale: 'zh-Hans', messages: zhHans });
        expect(html).toContain('确认删除');
        expect(html).toContain('取消');
        expect(html).not.toContain('budgets.confirmDelete');
    });
});

describe('BudgetRowActions 提交中', () => {
    it('删除请求在飞时确认按钮禁用(防连点重复提交)', () => {
        // en 的 confirmDelete 就是 Delete(与空闲态那颗同名, 靠确认提示区分),
        // 所以这里断言的是 disabled 属性真的落到确认按钮上 —— 防连点重复删就靠它。
        const html = renderSSR(<BudgetRowActions {...props} enabled deleting pending={{ id: 7, kind: 'delete' }} />);
        expect(html).toContain('Delete this budget?');
        expect(html).toContain('disabled');
    });

    it('启停请求在飞时确认按钮同样禁用', () => {
        const html = renderSSR(<BudgetRowActions {...props} enabled toggling pending={{ id: 7, kind: 'toggle' }} />);
        expect(html).toContain('disabled');
    });
});
