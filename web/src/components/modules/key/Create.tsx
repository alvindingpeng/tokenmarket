import { useState } from 'react';
import { Plus, Loader } from 'lucide-react';
import { motion, AnimatePresence } from 'motion/react';
import { useCreateAPIKey } from '@/api/endpoints/apikey';
import { Input } from '@/components/ui/input';

export function CreateCard() {
    const [name, setName] = useState('');
    const createAPIKey = useCreateAPIKey();

    const handleSubmit = (event: React.FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        if (!name.trim()) return;

        createAPIKey.mutate({ name: name.trim() }, {
            onSuccess: () => {
                setName('');
            }
        });
    };

    return (
        <div className="rounded-3xl border border-border bg-card p-6 custom-shadow">
            <form onSubmit={handleSubmit} className="flex items-center gap-4">
                <div className="flex-1">
                    <Input
                        type="text"
                        value={name}
                        onChange={(e) => setName(e.target.value)}
                        placeholder="名称"
                        disabled={createAPIKey.isPending}
                        className="w-full h-10 rounded-xl"
                    />
                </div>
                <button
                    type="submit"
                    disabled={createAPIKey.isPending || !name.trim()}
                    className="flex h-10 w-10 items-center justify-center rounded-xl bg-primary text-primary-foreground transition-all hover:bg-primary/90 active:scale-95 disabled:opacity-50 disabled:cursor-not-allowed"
                    title="创建 API Key"
                >
                    <AnimatePresence mode="wait">
                        {createAPIKey.isPending ? (
                            <motion.div
                                key="loading"
                                initial={{ scale: 0, rotate: -180 }}
                                animate={{ scale: 1, rotate: 0 }}
                                exit={{ scale: 0, rotate: 180 }}
                                transition={{ duration: 0.2 }}
                            >
                                <Loader className="h-5 w-5 animate-spin" />
                            </motion.div>
                        ) : (
                            <motion.div
                                key="plus"
                                initial={{ scale: 0, rotate: 180 }}
                                animate={{ scale: 1, rotate: 0 }}
                                exit={{ scale: 0, rotate: -180 }}
                                transition={{ duration: 0.2 }}
                            >
                                <Plus className="h-5 w-5" />
                            </motion.div>
                        )}
                    </AnimatePresence>
                </button>
            </form>
        </div>
    );
}
