import { useTranslations } from 'use-intl';
import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Building2, Plus, Edit, Trash2, Search, Loader2, X } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { MorphingDialog, MorphingDialogContainer, MorphingDialogContent, useMorphingDialog } from '@/components/ui/morphing-dialog';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { useTenants, useCreateTenant, useUpdateTenant, useDeleteTenant } from '@/api/tenant';
import { TenantDetailPanel } from './TenantDetailPanel';
import type { Tenant, CreateTenantRequest, UpdateTenantRequest, TenantStatus } from '@/api/tenant';
import { cn } from '@/lib/utils';

export default function TenantPage() {
    const t = useTranslations('tenant');
    const [search, setSearch] = useState('');
    const [editingTenant, setEditingTenant] = useState<Tenant | null>(null);
    const [detailTenant, setDetailTenant] = useState<Tenant | null>(null);
    const [formData, setFormData] = useState<CreateTenantRequest>({ name: '', code: '', status: 'active' });

    const { data: tenants = [], isLoading, refetch } = useQuery(useTenants());
    const createMutation = useCreateTenant();
    const updateMutation = useUpdateTenant();
    const deleteMutation = useDeleteTenant();

    const filteredTenants = tenants.filter(
        (tenant) =>
            tenant.name.toLowerCase().includes(search.toLowerCase()) ||
            tenant.code.toLowerCase().includes(search.toLowerCase())
    );

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();
        try {
            if (editingTenant) {
                await updateMutation.mutateAsync({ id: editingTenant.id, data: formData });
            } else {
                await createMutation.mutateAsync(formData);
            }
            setEditingTenant(null);
            resetForm();
            refetch();
        } catch (error) {
            console.error('Failed to save tenant:', error);
        }
    };

    const handleEdit = (tenant: Tenant) => {
        setEditingTenant(tenant);
        setFormData({ name: tenant.name, code: tenant.code, status: tenant.status });
    };

    const handleDelete = async (id: number) => {
        if (!confirm(t('deleteConfirm'))) return;
        try {
            await deleteMutation.mutateAsync(id);
            refetch();
        } catch (error) {
            console.error('Failed to delete tenant:', error);
        }
    };

    const handleNew = () => {
        setEditingTenant(null);
        resetForm();
    };

    const resetForm = () => {
        setFormData({ name: '', code: '', status: 'active' });
    };

    const getStatusBadge = (status: TenantStatus) => {
        const variants: Record<TenantStatus, 'default' | 'secondary' | 'destructive' | 'outline'> = {
            active: 'default',
            disabled: 'destructive',
            pending: 'secondary',
        };
        return <Badge variant={variants[status]}>{t(`status.${status}`)}</Badge>;
    };

    return (
        <div className="space-y-6">
            <div className="flex items-center justify-between">
                <CardTitle className="text-2xl">{t('title')}</CardTitle>
                <Button onClick={handleNew}>
                    <Plus className="mr-2 h-4 w-4" />
                    {t('create')}
                </Button>
            </div>

            <Card>
                <CardHeader>
                    <div className="flex items-center gap-4">
                        <div className="relative flex-1 max-w-md">
                            <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                            <Input
                                placeholder={t('searchPlaceholder')}
                                value={search}
                                onChange={(e) => setSearch(e.target.value)}
                                className="pl-10"
                            />
                        </div>
                    </div>
                </CardHeader>
                <CardContent>
                    {isLoading ? (
                        <div className="flex items-center justify-center py-8">
                            <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                        </div>
                    ) : (
                        <div className="overflow-x-auto">
                            <table className="w-full caption-bottom text-sm">
                                <thead>
                                    <tr className="border-b">
                                        <th className="h-12 px-4 text-left align-middle font-medium text-muted-foreground">
                                            {t('name')}
                                        </th>
                                        <th className="h-12 px-4 text-left align-middle font-medium text-muted-foreground">
                                            {t('code')}
                                        </th>
                                        <th className="h-12 px-4 text-left align-middle font-medium text-muted-foreground">
                                            {t('status')}
                                        </th>
                                        <th className="h-12 px-4 text-right align-middle font-medium text-muted-foreground">
                                            {t('actions')}
                                        </th>
                                    </tr>
                                </thead>
                                <tbody>
                                    {filteredTenants.length === 0 ? (
                                        <tr>
                                            <td className="p-4 text-center text-muted-foreground" colSpan={4}>
                                                {t('noTenants')}
                                            </td>
                                        </tr>
                                    ) : (
                                        filteredTenants.map((tenant) => (
                                            <tr key={tenant.id} className="border-b transition-colors hover:bg-muted/50 data-[state=selected]:bg-muted">
                                                <td className="p-4 font-medium">{tenant.name}</td>
                                                <td className="p-4">
                                                    <code className="text-sm bg-muted px-1.5 py-0.5 rounded">{tenant.code}</code>
                                                </td>
                                                <td className="p-4">{getStatusBadge(tenant.status)}</td>
                                                <td className="p-4 text-right">
                                                    <div className="flex items-center justify-end gap-2">
                                                        <Button
                                                            variant="ghost"
                                                            size="icon"
                                                            onClick={() => setDetailTenant(tenant)}
                                                            aria-label={t('details')}
                                                        >
                                                            <Building2 className="h-4 w-4" />
                                                        </Button>
                                                        <Button
                                                            variant="ghost"
                                                            size="icon"
                                                            onClick={() => handleEdit(tenant)}
                                                        >
                                                            <Edit className="h-4 w-4" />
                                                        </Button>
                                                        <Button
                                                            variant="ghost"
                                                            size="icon"
                                                            className="text-destructive hover:text-destructive hover:bg-destructive/10"
                                                            onClick={() => handleDelete(tenant.id)}
                                                        >
                                                            <Trash2 className="h-4 w-4" />
                                                        </Button>
                                                    </div>
                                                </td>
                                            </tr>
                                        ))
                                    )}
                                </tbody>
                            </table>
                        </div>
                    )}
                </CardContent>
            </Card>

            {editingTenant && (
                <MorphingDialog>
                    <MorphingDialogContainer>
                        <MorphingDialogContent className="max-w-lg max-h-[85vh] overflow-y-auto">
                            <header className="mb-4 flex items-center justify-between">
                                <h2 className="text-lg font-semibold">{editingTenant ? t('edit') : t('create')}</h2>
                                <Button variant="ghost" size="icon" onClick={() => setEditingTenant(null)}>
                                    <X className="h-4 w-4" />
                                </Button>
                            </header>
                            <form onSubmit={handleSubmit}>
                                <div className="grid gap-4 py-4">
                                    <div className="grid grid-cols-4 items-center gap-4">
                                        <Label htmlFor="name" className="text-right">
                                            {t('name')}
                                        </Label>
                                        <Input
                                            id="name"
                                            value={formData.name}
                                            onChange={(e) => setFormData({ ...formData, name: e.target.value })}
                                            placeholder={t('namePlaceholder')}
                                            className="col-span-3"
                                            required
                                        />
                                    </div>
                                    <div className="grid grid-cols-4 items-center gap-4">
                                        <Label htmlFor="code" className="text-right">
                                            {t('code')}
                                        </Label>
                                        <Input
                                            id="code"
                                            value={formData.code}
                                            onChange={(e) => setFormData({ ...formData, code: e.target.value })}
                                            placeholder={t('codePlaceholder')}
                                            className="col-span-3"
                                            required
                                        />
                                    </div>
                                    <div className="grid grid-cols-4 items-center gap-4">
                                        <Label htmlFor="status" className="text-right">
                                            {t('status')}
                                        </Label>
                                        <Select
                                            value={formData.status}
                                            onValueChange={(value) => setFormData({ ...formData, status: value as TenantStatus })}
                                        >
                                            <SelectTrigger className="col-span-3">
                                                <SelectValue />
                                            </SelectTrigger>
                                            <SelectContent>
                                                <SelectItem value="active">{t('status.active')}</SelectItem>
                                                <SelectItem value="disabled">{t('status.disabled')}</SelectItem>
                                                <SelectItem value="pending">{t('status.pending')}</SelectItem>
                                            </SelectContent>
                                        </Select>
                                    </div>
                                </div>
                                <footer className="mt-4 flex justify-end gap-2 border-t pt-4">
                                    <Button type="submit" disabled={createMutation.isPending || updateMutation.isPending}>
                                        {createMutation.isPending || updateMutation.isPending ? (
                                            <>
                                                <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                                                {t('saving')}
                                            </>
                                        ) : (
                                            t('save')
                                        )}
                                    </Button>
                                </footer>
                            </form>
                        </MorphingDialogContent>
                    </MorphingDialogContainer>
                </MorphingDialog>
            )}

            {detailTenant && (
                <MorphingDialog>
                    <MorphingDialogContainer>
                        <MorphingDialogContent className="max-w-2xl max-h-[85vh] overflow-y-auto">
                            <header className="mb-4 flex items-center justify-between">
                                <div>
                                    <h2 className="text-lg font-semibold">{detailTenant.name}</h2>
                                    <p className="text-xs text-muted-foreground">{detailTenant.code}</p>
                                </div>
                                <Button variant="ghost" size="icon" onClick={() => setDetailTenant(null)}>
                                    <X className="h-4 w-4" />
                                </Button>
                            </header>
                            <TenantDetailPanel tenant={detailTenant} />
                        </MorphingDialogContent>
                    </MorphingDialogContainer>
                </MorphingDialog>
            )}
        </div>
    );
}