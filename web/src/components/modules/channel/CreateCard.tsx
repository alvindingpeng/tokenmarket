import { Plus } from 'lucide-react';
import {
    MorphingDialog,
    MorphingDialogTrigger,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogClose,
    MorphingDialogTitle,
    MorphingDialogDescription,
} from '@/components/ui/morphing-dialog';
import { CreateForm } from './CreateForm';

export function CreateCard() {
    return (
        <MorphingDialog>
            <MorphingDialogTrigger className="w-full">
                <div className="rounded-2xl bg-primary p-6 min-h-[14rem] h-full flex flex-col items-center justify-center gap-4 custom-shadow hover:opacity-90 transition-all duration-300 hover:scale-[1.02]">
                    <div className="w-16 h-16 rounded-full bg-primary-foreground/20 flex items-center justify-center">
                        <Plus className="w-8 h-8 text-primary-foreground" />
                    </div>
                    <span className="text-primary-foreground font-semibold text-lg">创建渠道</span>
                </div>
            </MorphingDialogTrigger>

            <MorphingDialogContainer>
                <MorphingDialogContent className="w-[600px] bg-card rounded-3xl p-8 custom-shadow">
                    <MorphingDialogTitle>
                        <div className="flex items-center justify-between mb-6">
                            <h2 className="text-2xl font-bold text-card-foreground">创建新渠道</h2>
                            <MorphingDialogClose
                                className="relative top-0 right-0"
                                variants={{
                                    initial: { opacity: 0, scale: 0.8 },
                                    animate: { opacity: 1, scale: 1 },
                                    exit: { opacity: 0, scale: 0.8 }
                                }}
                            />
                        </div>
                    </MorphingDialogTitle>

                    <MorphingDialogDescription>
                        <CreateForm />
                    </MorphingDialogDescription>
                </MorphingDialogContent>
            </MorphingDialogContainer>
        </MorphingDialog>
    );
}
