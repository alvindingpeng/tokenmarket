import { useState } from 'react';
import { useCreateModel } from '@/api/model';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { Field, FieldLabel, FieldGroup } from '@/components/ui/field';
import {
    MorphingDialogDescription,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { parseLLMPriceForm } from '@/lib/price-input';

export function CreateDialogContent() {
    const { setIsOpen } = useMorphingDialog();
    const t = useTranslations('model.create');
    const createModel = useCreateModel();

    const [formData, setFormData] = useState({
        name: '',
        input: '',
        output: '',
        cache_read: '',
        cache_write: '',
    });

    const handleSubmit = (event: React.FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        if (!formData.name.trim()) return;

        // 价格先在本地校验再提交: 旧的 parseFloat(x) || 0 会把 "abc" 静默变成 0
        // (付费模型悄悄变成免费), 负价则原样送上去被后端拒 —— 两种都没有任何提示。
        const price = parseLLMPriceForm(formData);
        if (!price.ok) {
            toast.error(price.reason === 'too_large' ? t('priceTooLarge') : t('priceInvalid'));
            return;
        }

        createModel.mutate({
            name: formData.name.trim(),
            ...price.fields,
        }, {
            onSuccess: () => {
                setFormData({ name: '', input: '', output: '', cache_read: '', cache_write: '' });
                setIsOpen(false);
            },
            // 此前只有 onSuccess: 重名或校验失败时弹窗毫无反应, 用户只能反复点提交。
            onError: (error) => toast.error(t('toast.createFailed'), { description: error.message }),
        });
    };

    return (
        <div className="w-screen max-w-full md:max-w-xl">
            <MorphingDialogDescription>
                <form onSubmit={handleSubmit}>
                    <FieldGroup className="gap-4">
                        <Field>
                            <FieldLabel htmlFor="model-name">{t('name')}</FieldLabel>
                            <Input
                                id="model-name"
                                value={formData.name}
                                onChange={(e) => setFormData({ ...formData, name: e.target.value })}
                                className="rounded-xl"
                            />
                        </Field>
                        <div className="grid grid-cols-2 gap-4">
                            <Field>
                                <FieldLabel htmlFor="model-input">{t('input')}</FieldLabel>
                                <Input
                                    id="model-input"
                                    type="number"
                                    step="any"
                                    min="0"
                                    value={formData.input}
                                    onChange={(e) => setFormData({ ...formData, input: e.target.value })}
                                    className="rounded-xl"
                                />
                            </Field>
                            <Field>
                                <FieldLabel htmlFor="model-output">{t('output')}</FieldLabel>
                                <Input
                                    id="model-output"
                                    type="number"
                                    step="any"
                                    min="0"
                                    value={formData.output}
                                    onChange={(e) => setFormData({ ...formData, output: e.target.value })}
                                    className="rounded-xl"
                                />
                            </Field>
                            <Field>
                                <FieldLabel htmlFor="model-cache-read">{t('cacheRead')}</FieldLabel>
                                <Input
                                    id="model-cache-read"
                                    type="number"
                                    step="any"
                                    min="0"
                                    value={formData.cache_read}
                                    onChange={(e) => setFormData({ ...formData, cache_read: e.target.value })}
                                    className="rounded-xl"
                                />
                            </Field>
                            <Field>
                                <FieldLabel htmlFor="model-cache-write">{t('cacheWrite')}</FieldLabel>
                                <Input
                                    id="model-cache-write"
                                    type="number"
                                    step="any"
                                    min="0"
                                    value={formData.cache_write}
                                    onChange={(e) => setFormData({ ...formData, cache_write: e.target.value })}
                                    className="rounded-xl"
                                />
                            </Field>
                        </div>
                        <div className="flex gap-2">
                            <Button
                                type="button"
                                variant="secondary"
                                onClick={() => setIsOpen(false)}
                                className="flex-1 rounded-xl h-11"
                            >
                                {t('cancel')}
                            </Button>
                            <Button
                                type="submit"
                                disabled={createModel.isPending || !formData.name.trim()}
                                className="flex-1 rounded-xl h-11"
                            >
                                {createModel.isPending ? t('submitting') : t('submit')}
                            </Button>
                        </div>
                    </FieldGroup>
                </form>
            </MorphingDialogDescription>
        </div>
    );
}
