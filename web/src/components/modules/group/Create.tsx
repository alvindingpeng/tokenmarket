'use client';

import type { GroupItem } from '@/api/endpoints/group';
import {
    MorphingDialogClose,
    MorphingDialogTitle,
    MorphingDialogDescription,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';
import { useCreateGroup } from '@/api/endpoints/group';
import { useTranslations } from 'next-intl';
import { GroupEditor } from './Editor';
import { toast } from '@/components/common/Toast';

export function CreateDialogContent() {
    const { setIsOpen } = useMorphingDialog();
    const createGroup = useCreateGroup();
    const t = useTranslations('group');

    return (
        <div className="w-screen max-w-full md:max-w-4xl">
            <MorphingDialogTitle>
                <header className="mb-5 flex items-center justify-between">
                    <h2 className="text-2xl font-bold text-card-foreground">
                        {t('create.title')}
                    </h2>
                    <MorphingDialogClose
                        className="relative right-0 top-0"
                        variants={{
                            initial: { opacity: 0, scale: 0.8 },
                            animate: { opacity: 1, scale: 1 },
                            exit: { opacity: 0, scale: 0.8 },
                        }}
                    />
                </header>
            </MorphingDialogTitle>
            <MorphingDialogDescription>
                <GroupEditor
                    submitText={t('create.submit')}
                    submittingText={t('create.submitting')}
                    isSubmitting={createGroup.isPending}
                    onSubmit={({ name, match_regex, mode, members }) => {
                        const items: GroupItem[] = members.map((member, index) => ({
                            channel_id: member.channel_id,
                            model_name: member.name,
                            priority: index + 1,
                            weight: member.weight ?? 1,
                        }));

                        createGroup.mutate(
                            { name, mode, match_regex: match_regex ?? '', items },
                            {
                                onSuccess: () => setIsOpen(false),
                                onError: (error) => toast.error(t('toast.createFailed'), { description: error.message }),
                            }
                        );
                    }}
                />
            </MorphingDialogDescription>
        </div>
    );
}
