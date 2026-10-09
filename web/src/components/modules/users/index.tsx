import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { AlertCircle, KeyRound, Loader2, Pencil, Plus, Trash2, UserRound } from 'lucide-react';
import { toast } from 'sonner';
import { useCreateUser, useDeleteUser, useResetPassword, useUpdateUser, useUserList, type UserManageRequest } from '@/api/usermanage';
import type { Role, UserView } from '@/api/user';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { IconButton } from '@/components/common/IconButton';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { MorphingDialog, MorphingDialogContainer, MorphingDialogContent, MorphingDialogDescription, MorphingDialogTrigger, useMorphingDialog } from '@/components/ui/morphing-dialog';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';

// ROLE_OPTIONS 是用户角色的可选项; 顺序与后端 Role 枚举一致。
const ROLE_OPTIONS: Role[] = ['admin', 'reseller', 'user'];

// USER_STATUSES 是用户状态的可选项。
const USER_STATUSES: UserManageRequest['status'][] = ['active', 'pending', 'disabled'];

// RoleBadge 渲染角色徽标: 管理员红, 渠道商蓝, 普通用户用默认样式。
function RoleBadge({ role }: { role: Role }) {
    const t = useTranslations('users');
    const className = role === 'admin'
        ? 'bg-red-500/10 text-red-600 dark:text-red-400'
        : role === 'reseller'
            ? 'bg-blue-500/10 text-blue-600 dark:text-blue-400'
            : '';

    return (
        <Badge variant={role === 'user' ? 'secondary' : 'outline'} className={className}>
            {t('role.' + role)}
        </Badge>
    );
}

// StatusBadge 渲染状态徽标: 启用绿, 待审黄, 停用灰。
function StatusBadge({ status }: { status: UserView['status'] }) {
    const t = useTranslations('users');
    const className = status === 'active'
        ? 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'
        : status === 'pending'
            ? 'bg-amber-500/10 text-amber-600 dark:text-amber-400'
            : 'bg-muted text-muted-foreground';

    return (
        <Badge variant="outline" className={className}>
            {t('status.' + status)}
        </Badge>
    );
}

// formatMoney 将金额格式化为两位小数的美元金额。
function formatMoney(value: number) {
    return '$' + value.toFixed(2);
}

// formatCreatedAt 将后端 RFC3339 时间格式化为本地日期; 零值时间显示占位符。
function formatCreatedAt(value: string, fallback: string) {
    const date = new Date(value);
    if (Number.isNaN(date.getTime()) || date.getUTCFullYear() === 1) return fallback;
    return date.toLocaleDateString();
}

// CreateDialogContent 提供创建用户表单; 密码必填, 状态默认启用。
function CreateDialogContent() {
    const t = useTranslations('users');
    const { setIsOpen } = useMorphingDialog();
    const createUser = useCreateUser();
    const [username, setUsername] = useState('');
    const [password, setPassword] = useState('');
    const [role, setRole] = useState<Role>('user');
    const [status, setStatus] = useState<UserManageRequest['status']>('active');

    // 用户名与密码都非空才允许提交, 与后端校验保持一致。
    const canSubmit = username.trim() !== '' && password !== '';

    const handleSubmit = (event: React.FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        if (!canSubmit || createUser.isPending) return;

        createUser.mutate(
            { username: username.trim(), password, role, status },
            {
                onSuccess: () => {
                    toast.success(t('toast.created'));
                    setIsOpen(false);
                },
                onError: (error) => toast.error(t('toast.createFailed'), { description: error.message }),
            },
        );
    };

    return (
        <div className="w-screen max-w-full md:max-w-md">
            <MorphingDialogDescription>
                <form onSubmit={handleSubmit} className="space-y-4">
                    <div className="space-y-2">
                        <Label htmlFor="user-create-username">{t('form.username')}</Label>
                        <Input
                            id="user-create-username"
                            value={username}
                            onChange={(e) => setUsername(e.target.value)}
                            className="rounded-xl"
                            autoComplete="off"
                        />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor="user-create-password">{t('form.password')}</Label>
                        <Input
                            id="user-create-password"
                            type="password"
                            value={password}
                            onChange={(e) => setPassword(e.target.value)}
                            className="rounded-xl"
                            autoComplete="new-password"
                        />
                    </div>
                    <div className="grid grid-cols-2 gap-4">
                        <div className="space-y-2">
                            <Label>{t('form.role')}</Label>
                            <Select value={role} onValueChange={(value) => setRole(value as Role)}>
                                <SelectTrigger className="w-full rounded-xl">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent className="rounded-xl">
                                    {ROLE_OPTIONS.map((option) => (
                                        <SelectItem key={option} value={option} className="rounded-xl">
                                            {t('role.' + option)}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                        <div className="space-y-2">
                            <Label>{t('form.status')}</Label>
                            <Select
                                value={status}
                                onValueChange={(value) => setStatus(value as UserManageRequest['status'])}
                            >
                                <SelectTrigger className="w-full rounded-xl">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent className="rounded-xl">
                                    {USER_STATUSES.map((option) => (
                                        <SelectItem key={option} value={option} className="rounded-xl">
                                            {t('status.' + option)}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                    </div>
                    <div className="flex justify-end gap-2 pt-2">
                        <Button
                            type="button"
                            variant="secondary"
                            onClick={() => setIsOpen(false)}
                            className="rounded-xl h-9 px-4"
                        >
                            {t('form.cancel')}
                        </Button>
                        <Button type="submit" disabled={createUser.isPending || !canSubmit} className="rounded-xl h-9 px-4">
                            {createUser.isPending ? t('form.submitting') : t('form.submit')}
                        </Button>
                    </div>
                </form>
            </MorphingDialogDescription>
        </div>
    );
}

// CreateUserButton 是工具栏上的创建用户入口。
function CreateUserButton() {
    const t = useTranslations('users');
    return (
        <MorphingDialog>
            <MorphingDialogTrigger className="flex h-9 items-center gap-1.5 rounded-xl bg-primary px-4 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90">
                <Plus className="size-4" />
                {t('create')}
            </MorphingDialogTrigger>
            <MorphingDialogContainer>
                <MorphingDialogContent
                    dismissOnClickOutside={false}
                    className="flex max-h-[calc(100dvh-2rem)] w-fit max-w-full flex-col overflow-hidden rounded-3xl bg-card px-6 py-5 text-card-foreground"
                >
                    <CreateDialogContent />
                </MorphingDialogContent>
            </MorphingDialogContainer>
        </MorphingDialog>
    );
}

// EditUserDialogContent 提供编辑表单; 密码留空表示不修改。
function EditUserDialogContent({ user }: { user: UserView }) {
    const t = useTranslations('users');
    const { setIsOpen } = useMorphingDialog();
    const updateUser = useUpdateUser();
    const [username, setUsername] = useState(user.username);
    const [password, setPassword] = useState('');
    const [role, setRole] = useState<Role>(user.role);
    const [status, setStatus] = useState<UserManageRequest['status']>(user.status);
    // 余额按绝对值调整; 与原值一致时不下发, 避免产生无意义的审计记录。
    const [balance, setBalance] = useState(String(user.balance));

    const canSubmit = username.trim() !== '';

    const handleSubmit = (event: React.FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        if (!canSubmit || updateUser.isPending) return;

        const request: UserManageRequest = {
            id: user.id,
            username: username.trim(),
            role,
            status,
        };
        // 空密码不下发, 后端据此保留原密码。
        if (password !== '') request.password = password;
        const parsedBalance = Number(balance);
        if (balance.trim() !== '' && Number.isFinite(parsedBalance) && parsedBalance >= 0 && parsedBalance !== user.balance) {
            request.balance = parsedBalance;
        }

        updateUser.mutate(request, {
            onSuccess: () => {
                toast.success(t('toast.updated'));
                setIsOpen(false);
            },
            onError: (error) => toast.error(t('toast.updateFailed'), { description: error.message }),
        });
    };

    return (
        <div className="w-screen max-w-full md:max-w-md">
            <MorphingDialogDescription>
                <form onSubmit={handleSubmit} className="space-y-4">
                    <div className="space-y-2">
                        <Label htmlFor={'user-edit-username-' + user.id}>{t('form.username')}</Label>
                        <Input
                            id={'user-edit-username-' + user.id}
                            value={username}
                            onChange={(e) => setUsername(e.target.value)}
                            className="rounded-xl"
                        />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor={'user-edit-password-' + user.id}>{t('form.passwordOptional')}</Label>
                        <Input
                            id={'user-edit-password-' + user.id}
                            type="password"
                            value={password}
                            onChange={(e) => setPassword(e.target.value)}
                            placeholder={t('form.passwordUnchanged')}
                            className="rounded-xl"
                            autoComplete="new-password"
                        />
                    </div>
                    <div className="space-y-2">
                        <Label htmlFor={'user-edit-balance-' + user.id}>{t('form.balance')}</Label>
                        <Input
                            id={'user-edit-balance-' + user.id}
                            type="number"
                            min={0}
                            step="any"
                            value={balance}
                            onChange={(e) => setBalance(e.target.value)}
                            className="rounded-xl"
                        />
                        <p className="text-xs text-muted-foreground">{t('form.balanceHint')}</p>
                    </div>
                    <div className="grid grid-cols-2 gap-4">
                        <div className="space-y-2">
                            <Label>{t('form.role')}</Label>
                            <Select value={role} onValueChange={(value) => setRole(value as Role)}>
                                <SelectTrigger className="w-full rounded-xl">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent className="rounded-xl">
                                    {ROLE_OPTIONS.map((option) => (
                                        <SelectItem key={option} value={option} className="rounded-xl">
                                            {t('role.' + option)}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                        <div className="space-y-2">
                            <Label>{t('form.status')}</Label>
                            <Select
                                value={status}
                                onValueChange={(value) => setStatus(value as UserManageRequest['status'])}
                            >
                                <SelectTrigger className="w-full rounded-xl">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent className="rounded-xl">
                                    {USER_STATUSES.map((option) => (
                                        <SelectItem key={option} value={option} className="rounded-xl">
                                            {t('status.' + option)}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                    </div>
                    <div className="flex justify-end gap-2 pt-2">
                        <Button
                            type="button"
                            variant="secondary"
                            onClick={() => setIsOpen(false)}
                            className="rounded-xl h-9 px-4"
                        >
                            {t('form.cancel')}
                        </Button>
                        <Button type="submit" disabled={updateUser.isPending || !canSubmit} className="rounded-xl h-9 px-4">
                            {updateUser.isPending ? t('form.saving') : t('form.save')}
                        </Button>
                    </div>
                </form>
            </MorphingDialogDescription>
        </div>
    );
}

// ResetPasswordDialogContent 让管理员为指定用户设置新密码。
function ResetPasswordDialogContent({ user }: { user: UserView }) {
    const t = useTranslations('users');
    const { setIsOpen } = useMorphingDialog();
    const resetPassword = useResetPassword();
    const [password, setPassword] = useState('');

    const canSubmit = password !== '';

    const handleSubmit = (event: React.FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        if (!canSubmit || resetPassword.isPending) return;

        resetPassword.mutate({ id: user.id, password }, {
            onSuccess: () => {
                toast.success(t('toast.passwordReset'));
                setIsOpen(false);
            },
            onError: (error) => toast.error(t('toast.resetFailed'), { description: error.message }),
        });
    };

    return (
        <div className="w-screen max-w-full md:max-w-md">
            <MorphingDialogDescription>
                <form onSubmit={handleSubmit} className="space-y-4">
                    <p className="text-sm text-muted-foreground">
                        {t('reset.description', { username: user.username })}
                    </p>
                    <div className="space-y-2">
                        <Label htmlFor={'user-reset-password-' + user.id}>{t('reset.newPassword')}</Label>
                        <Input
                            id={'user-reset-password-' + user.id}
                            type="password"
                            value={password}
                            onChange={(e) => setPassword(e.target.value)}
                            className="rounded-xl"
                            autoComplete="new-password"
                        />
                    </div>
                    <div className="flex justify-end gap-2 pt-2">
                        <Button
                            type="button"
                            variant="secondary"
                            onClick={() => setIsOpen(false)}
                            className="rounded-xl h-9 px-4"
                        >
                            {t('form.cancel')}
                        </Button>
                        <Button type="submit" disabled={resetPassword.isPending || !canSubmit} className="rounded-xl h-9 px-4">
                            {resetPassword.isPending ? t('reset.resetting') : t('reset.submit')}
                        </Button>
                    </div>
                </form>
            </MorphingDialogDescription>
        </div>
    );
}

// DeleteUserDialogContent 二次确认删除; 危险操作使用 destructive 按钮。
function DeleteUserDialogContent({ user }: { user: UserView }) {
    const t = useTranslations('users');
    const { setIsOpen } = useMorphingDialog();
    const deleteUser = useDeleteUser();

    const handleDelete = () => {
        if (deleteUser.isPending) return;
        deleteUser.mutate(user.id, {
            onSuccess: () => {
                toast.success(t('toast.deleted'));
                setIsOpen(false);
            },
            onError: (error) => toast.error(t('toast.deleteFailed'), { description: error.message }),
        });
    };

    return (
        <div className="w-screen max-w-full md:max-w-md">
            <MorphingDialogDescription>
                <div className="space-y-5">
                    <p className="text-sm text-muted-foreground">
                        {t('delete.description', { username: user.username })}
                    </p>
                    <div className="flex justify-end gap-2">
                        <Button
                            type="button"
                            variant="secondary"
                            onClick={() => setIsOpen(false)}
                            className="rounded-xl h-9 px-4"
                        >
                            {t('form.cancel')}
                        </Button>
                        <Button variant="destructive" onClick={handleDelete} disabled={deleteUser.isPending} className="rounded-xl h-9 px-4">
                            {deleteUser.isPending ? t('delete.deleting') : t('delete.submit')}
                        </Button>
                    </div>
                </div>
            </MorphingDialogDescription>
        </div>
    );
}

// RowDialog 承载单行的编辑, 重置密码与删除弹窗, 避免在表格里重复三段弹窗骨架。
function RowDialog({ trigger, children }: { trigger: React.ReactNode; children: React.ReactNode }) {
    return (
        <MorphingDialog>
            <MorphingDialogTrigger className="inline-flex">{trigger}</MorphingDialogTrigger>
            <MorphingDialogContainer>
                <MorphingDialogContent
                    dismissOnClickOutside={false}
                    className="flex max-h-[calc(100dvh-2rem)] w-fit max-w-full flex-col overflow-hidden rounded-3xl bg-card px-6 py-5 text-card-foreground"
                >
                    {children}
                </MorphingDialogContent>
            </MorphingDialogContainer>
        </MorphingDialog>
    );
}

// UsersTable 渲染用户清单; 窄屏下横向滚动, 保留全部列。
function UsersTable({ users }: { users: UserView[] }) {
    const t = useTranslations('users');

    return (
        <div className="overflow-x-auto rounded-3xl border border-border bg-card">
            <table className="w-full min-w-[52rem] border-collapse text-sm">
                <thead>
                    <tr className="border-b border-border text-left text-xs text-muted-foreground">
                        <th className="px-4 py-3 font-medium">{t('table.username')}</th>
                        <th className="px-4 py-3 font-medium">{t('table.role')}</th>
                        <th className="px-4 py-3 font-medium">{t('table.status')}</th>
                        <th className="px-4 py-3 text-right font-medium">{t('table.balance')}</th>
                        <th className="px-4 py-3 text-right font-medium">{t('table.frozen')}</th>
                        <th className="px-4 py-3 text-right font-medium">{t('table.totalSpent')}</th>
                        <th className="px-4 py-3 text-right font-medium">{t('table.totalRevenue')}</th>
                        <th className="px-4 py-3 font-medium">{t('table.createdAt')}</th>
                        <th className="px-4 py-3 text-right font-medium">{t('table.actions')}</th>
                    </tr>
                </thead>
                <tbody>
                    {users.map((user) => (
                        <tr key={user.id} className="border-b border-border last:border-0">
                            <td className="px-4 py-3 font-medium text-card-foreground">{user.username}</td>
                            <td className="px-4 py-3"><RoleBadge role={user.role} /></td>
                            <td className="px-4 py-3"><StatusBadge status={user.status} /></td>
                            <td className="px-4 py-3 text-right tabular-nums">{formatMoney(user.balance)}</td>
                            <td className="px-4 py-3 text-right tabular-nums text-muted-foreground">{formatMoney(user.frozen)}</td>
                            <td className="px-4 py-3 text-right tabular-nums">{formatMoney(user.total_spent)}</td>
                            <td className="px-4 py-3 text-right tabular-nums">{formatMoney(user.total_revenue)}</td>
                            <td className="px-4 py-3 whitespace-nowrap text-muted-foreground">
                                {formatCreatedAt(user.created_at, t('table.unknownDate'))}
                            </td>
                            <td className="px-4 py-3">
                                <div className="flex items-center justify-end gap-1">
                                    <RowDialog trigger={<IconButton tip={t('action.edit')} className="size-8 rounded-lg"><Pencil className="size-4" /></IconButton>}>
                                        <EditUserDialogContent user={user} />
                                    </RowDialog>
                                    <RowDialog trigger={<IconButton tip={t('action.resetPassword')} className="size-8 rounded-lg"><KeyRound className="size-4" /></IconButton>}>
                                        <ResetPasswordDialogContent user={user} />
                                    </RowDialog>
                                    <RowDialog trigger={<IconButton tip={t('action.delete')} className="size-8 rounded-lg hover:text-destructive"><Trash2 className="size-4" /></IconButton>}>
                                        <DeleteUserDialogContent user={user} />
                                    </RowDialog>
                                </div>
                            </td>
                        </tr>
                    ))}
                </tbody>
            </table>
        </div>
    );
}

// Users 渲染管理员用户管理页面正文。
export function Users() {
    const t = useTranslations('users');
    const { data: users, isLoading, isError, error, refetch } = useUserList();

    return (
        <div className="flex h-full min-h-0 flex-col gap-3">
            <div className="flex shrink-0 items-center justify-between gap-4">
                <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                    <UserRound className="size-5" />
                    {t('title')}
                </h2>
                <CreateUserButton />
            </div>

            <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain pb-24 md:pb-4">
                {isLoading ? (
                    <div className="flex h-40 items-center justify-center">
                        <Loader2 className="size-6 animate-spin text-muted-foreground" />
                    </div>
                ) : isError ? (
                    <div className="flex h-40 flex-col items-center justify-center gap-2 text-sm text-destructive">
                        <AlertCircle className="size-6" />
                        <span>{t('list.error')}</span>
                        <span className="text-xs text-muted-foreground">{error.message}</span>
                        <Button variant="outline" size="sm" onClick={() => void refetch()} className="rounded-xl">
                            {t('list.retry')}
                        </Button>
                    </div>
                ) : (users?.length ?? 0) === 0 ? (
                    <div className="flex h-40 flex-col items-center justify-center gap-3 text-muted-foreground">
                        <UserRound className="size-8" />
                        <span className="text-sm">{t('list.empty')}</span>
                    </div>
                ) : (
                    <UsersTable users={users ?? []} />
                )}
            </div>
        </div>
    );
}

// UsersActions 向工具栏提供创建用户入口; 与其他页面的 Actions 一样由外层按页挂载。
export function UsersActions() {
    const t = useTranslations('users');
    return (
        <div className="flex h-9 items-center gap-2">
            <span className="hidden text-sm font-medium text-muted-foreground md:inline">{t('title')}</span>
            <CreateUserButton />
        </div>
    );
}

// 页面默认导出, 便于按模块默认导入。
export default function UsersModule() {
    return <Users />;
}