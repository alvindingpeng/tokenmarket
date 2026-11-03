import type { UserManageRequest } from '@/api/usermanage';
import type { Role, UserView } from '@/api/user';
import type { UserTier } from '@/lib/user-tier';

// 用户创建/编辑表单的请求体构造逻辑。
//
// 为什么从组件里抽出来: "哪些字段该发、哪些该留空"是几条非平凡规则
// (密码留空=不改、邮箱留空=不改、余额不变=不发、等级不变=不发),
// 写在 JSX 里既没法单测, 又容易在两份表单之间漂移。
// 抽成纯函数后, 规则本身可以被逐条钉在测试里。

export type CreateUserInput = {
    username: string;
    email: string;
    password: string;
    role: Role;
    status: UserManageRequest['status'];
    tier: UserTier;
};

// PasswordMinLength 与后端 model.PasswordMinLen 对齐。
// 后端在 op/user.go 里对创建、编辑、重置三处都强制这个下限, 而前端此前只判非空 ——
// 管理员填 6 位点提交, 必定被后端拒, 看到的还是英文原文
// "password must be at least 8 characters"。校验放在提交前, 提示才能是本地化的。
export const PasswordMinLength = 8;

export type PasswordProblem = 'too_short' | null;

// passwordProblem 返回密码不合格的原因, 空串表示"本次不设密码"(创建时必填, 由调用方判)。
export function passwordProblem(password: string): PasswordProblem {
    if (password === '') return null;
    if (password.length < PasswordMinLength) return 'too_short';
    return null;
}

// buildCreateUserRequest 创建请求: 等级总是随请求下发(新用户必须有明确等级)。
export function buildCreateUserRequest(input: CreateUserInput): UserManageRequest {
    return {
        username: input.username.trim(),
        email: input.email.trim(),
        password: input.password,
        role: input.role,
        status: input.status,
        tier: input.tier,
    };
}

export type UpdateUserInput = {
    user: UserView;
    username: string;
    email: string;
    password: string;
    role: Role;
    status: UserManageRequest['status'];
    balance: string;
    // tier 空串 = 保持原等级(存量账号未设置时不能强行改成一档)。
    tier: UserTier | '';
};

// buildUpdateUserRequest 编辑请求: 只下发真的变了的字段。
// 每条"与原值相同就不发"都是刻意的: 无变化的写入会污染审计记录,
// 也会让管理员分不清"我到底改没改这一项"。
export function buildUpdateUserRequest(input: UpdateUserInput): UserManageRequest {
    const request: UserManageRequest = {
        id: input.user.id,
        username: input.username.trim(),
        role: input.role,
        status: input.status,
    };
    // 空密码/空邮箱不下发, 后端据此保留原值。
    if (input.password !== '') request.password = input.password;
    if (input.email.trim() !== '') request.email = input.email.trim();

    const parsedBalance = Number(input.balance);
    if (
        input.balance.trim() !== ''
        && Number.isFinite(parsedBalance)
        && parsedBalance >= 0
        && parsedBalance !== input.user.balance
    ) {
        request.balance = parsedBalance;
    }

    // 等级只在真的换了才下发。
    if (input.tier !== '' && input.tier !== input.user.tier) request.tier = input.tier;

    return request;
}
