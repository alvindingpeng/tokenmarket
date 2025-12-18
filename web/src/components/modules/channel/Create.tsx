import { useState } from 'react';
import {
    MorphingDialogClose,
    MorphingDialogTitle,
    MorphingDialogDescription,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';
import { useCreateChannel, ChannelType } from '@/api/endpoints/channel';
import { useTranslations } from 'next-intl';
import { ChannelForm, type ChannelFormData } from './Form';

export function CreateDialogContent() {
    const { setIsOpen } = useMorphingDialog();
    const createChannel = useCreateChannel();
    const [formData, setFormData] = useState<ChannelFormData>({
        name: '',
        type: ChannelType.OpenAIChat,
        base_url: '',
        key: '',
        model: '',
        custom_model: '',
        auto_sync: false,
        auto_group: false,
        enabled: true,
        proxy: false,
    });
    const t = useTranslations('channel.create');

    const handleSubmit = (event: React.FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        createChannel.mutate(formData, {
            onSuccess: () => {
                setFormData({
                    name: '',
                    type: ChannelType.OpenAIChat,
                    base_url: '',
                    key: '',
                    model: '',
                    custom_model: '',
                    auto_sync: false,
                    auto_group: false,
                    enabled: true,
                    proxy: false,
                });
                setIsOpen(false);
            }
        });
    };

    return (
        <>
            <MorphingDialogTitle>
                <header className="mb-6 flex items-center justify-between">
                    <h2 className="text-2xl font-bold text-card-foreground">{t('dialogTitle')}</h2>
                    <MorphingDialogClose
                        className="relative right-0 top-0"
                        variants={{
                            initial: { opacity: 0, scale: 0.8 },
                            animate: { opacity: 1, scale: 1 },
                            exit: { opacity: 0, scale: 0.8 }
                        }}
                    />
                </header>
            </MorphingDialogTitle>
            <MorphingDialogDescription>
                <ChannelForm
                    formData={formData}
                    onFormDataChange={setFormData}
                    onSubmit={handleSubmit}
                    isPending={createChannel.isPending}
                    submitText={t('submit')}
                    pendingText={t('submitting')}
                    idPrefix="new-channel"
                />
            </MorphingDialogDescription>
        </>
    );
}
