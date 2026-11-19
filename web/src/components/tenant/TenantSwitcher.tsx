import { useMemo, useState } from 'react';
import { Building2, ChevronDown, Check } from 'lucide-react';
import { useAppStore } from '@/stores/app';
import { useAuthStore } from '@/api/user';
import type { Tenant } from '@/api/tenant';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { cn } from '@/lib/utils';

interface TenantSwitcherProps {
    className?: string;
    showLabel?: boolean;
}

// TenantSwitcher 管理员的租户视图切换器。
//
// 只读展示时(仅一个租户/非管理员)退化成静态徽标: 展示一个点不开的门比不展示更糟。
// 切换本身不只是改本地选中项 —— api/client 会把当前租户作为 X-Tenant-ID 带给后端,
// 后端的鉴权与数据作用域据此落到目标租户, 二者必须一起变, 否则界面与数据会脱节。
export function TenantSwitcher({ className, showLabel = true }: TenantSwitcherProps) {
    const { currentTenant, availableTenants, setCurrentTenant } = useAppStore();
    const role = useAuthStore((state) => state.role);
    const [open, setOpen] = useState(false);

    const sortedTenants = useMemo(
        () => [...availableTenants].sort((a, b) => a.name.localeCompare(b.name)),
        [availableTenants]
    );

    // 普通用户的租户由后端身份决定, 切换不会改变任何请求的落点, 因此不给可点的下拉。
    const canSwitch = role === 'admin' && sortedTenants.length > 1;

    const handleSelect = (tenant: Tenant) => {
        setCurrentTenant(tenant);
        setOpen(false);
    };

    if (!canSwitch) {
        return (
            <div className={cn('flex items-center gap-2 px-3 py-1.5 rounded-lg bg-muted/50', className)}>
                <Building2 className="h-4 w-4 text-muted-foreground" />
                {showLabel && (
                    <span className="text-sm font-medium text-muted-foreground truncate max-w-[160px]">
                        {currentTenant?.name || 'Default Tenant'}
                    </span>
                )}
            </div>
        );
    }

    return (
        <Popover open={open} onOpenChange={setOpen}>
            <PopoverTrigger asChild>
                <button
                    type="button"
                    className={cn(
                        'flex items-center gap-2 px-3 py-1.5 rounded-lg bg-muted/50 hover:bg-muted transition-colors',
                        'text-sm font-medium',
                        className
                    )}
                    aria-haspopup="listbox"
                    aria-expanded={open}
                >
                    <Building2 className="h-4 w-4 text-muted-foreground" />
                    {showLabel && (
                        <span className="truncate max-w-[160px]">
                            {currentTenant?.name || 'Select Tenant'}
                        </span>
                    )}
                    <ChevronDown
                        className={cn('h-4 w-4 text-muted-foreground transition-transform', open && 'rotate-180')}
                    />
                </button>
            </PopoverTrigger>
            <PopoverContent align="end" className="w-56 p-1">
                <div role="listbox" aria-label="Tenants">
                    {sortedTenants.map((tenant) => (
                        <button
                            key={tenant.id}
                            type="button"
                            onClick={() => handleSelect(tenant)}
                            role="option"
                            aria-selected={currentTenant?.id === tenant.id}
                            className={cn(
                                'flex items-center gap-2 w-full px-2 py-1.5 rounded text-sm text-left',
                                'hover:bg-accent transition-colors',
                                currentTenant?.id === tenant.id
                                    ? 'bg-accent font-medium text-accent-foreground'
                                    : 'text-foreground'
                            )}
                        >
                            <span className="truncate flex-1">{tenant.name}</span>
                            {tenant.code && (
                                <span className="text-xs text-muted-foreground px-1.5 py-0.5 rounded bg-muted">
                                    {tenant.code}
                                </span>
                            )}
                            {tenant.status !== 'active' && (
                                <span className="text-xs px-1.5 py-0.5 rounded bg-destructive/10 text-destructive">
                                    {tenant.status}
                                </span>
                            )}
                            {currentTenant?.id === tenant.id && <Check className="h-3.5 w-3.5 text-current" />}
                        </button>
                    ))}
                </div>
            </PopoverContent>
        </Popover>
    );
}
