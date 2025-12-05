import { useState } from 'react';
import { Copy, Trash2, Check, X } from 'lucide-react';
import { motion, AnimatePresence } from 'motion/react';
import { useDeleteAPIKey, type APIKey } from '@/api/endpoints/apikey';

interface KeyItemProps {
    apiKey: APIKey;
}

export function KeyItem({ apiKey }: KeyItemProps) {
    const [copied, setCopied] = useState(false);
    const [confirmDelete, setConfirmDelete] = useState(false);
    const deleteAPIKey = useDeleteAPIKey();

    const handleCopy = async () => {
        try {
            if (navigator.clipboard && window.isSecureContext) {
                await navigator.clipboard.writeText(apiKey.api_key);
            } else {
                const textArea = document.createElement('textarea');
                textArea.value = apiKey.api_key;
                textArea.style.position = 'fixed';
                textArea.style.left = '-9999px';
                document.body.appendChild(textArea);
                textArea.select();
                document.execCommand('copy');
                document.body.removeChild(textArea);
            }
            setCopied(true);
            setTimeout(() => setCopied(false), 2000);
        } catch (err) {
            console.error('Failed to copy:', err);
        }
    };

    const handleDeleteClick = () => {
        setConfirmDelete(true);
    };

    const handleConfirmDelete = () => {
        deleteAPIKey.mutate(apiKey.id);
    };

    const handleCancelDelete = () => {
        setConfirmDelete(false);
    };

    return (
        <div className="group relative rounded-3xl border border-border bg-card custom-shadow transition-all duration-300 hover:scale-[1.02] overflow-hidden">
            <div className="flex items-center justify-between gap-4 p-6">
                <div className="flex-1 min-w-0">
                    <h3 className="text-lg font-bold text-card-foreground">
                        {apiKey.name}
                    </h3>
                </div>

                <div className="flex items-center gap-2">
                    <button
                        onClick={handleCopy}
                        className="flex h-10 w-10 items-center justify-center rounded-xl bg-primary/10 text-primary transition-all hover:bg-primary hover:text-primary-foreground active:scale-95"
                        title="复制 API Key"
                    >
                        <AnimatePresence mode="wait">
                            {copied ? (
                                <motion.div
                                    key="check"
                                    initial={{ scale: 0, rotate: -180 }}
                                    animate={{ scale: 1, rotate: 0 }}
                                    exit={{ scale: 0, rotate: 180 }}
                                    transition={{ duration: 0.2 }}
                                >
                                    <Check className="h-5 w-5" />
                                </motion.div>
                            ) : (
                                <motion.div
                                    key="copy"
                                    initial={{ scale: 0, rotate: 180 }}
                                    animate={{ scale: 1, rotate: 0 }}
                                    exit={{ scale: 0, rotate: -180 }}
                                    transition={{ duration: 0.2 }}
                                >
                                    <Copy className="h-5 w-5" />
                                </motion.div>
                            )}
                        </AnimatePresence>
                    </button>

                    {!confirmDelete && (
                        <motion.button
                            layoutId={`delete-btn-${apiKey.id}`}
                            onClick={handleDeleteClick}
                            className="flex h-10 w-10 items-center justify-center rounded-xl bg-destructive/10 text-destructive transition-colors hover:bg-destructive hover:text-destructive-foreground"
                            title="删除"
                        >
                            <Trash2 className="h-5 w-5" />
                        </motion.button>
                    )}
                </div>
            </div>

            <AnimatePresence>
                {confirmDelete && (
                    <motion.div
                        layoutId={`delete-btn-${apiKey.id}`}
                        className="absolute inset-0 flex items-center justify-center gap-3 bg-destructive p-6 rounded-3xl"
                        transition={{ type: 'spring', stiffness: 400, damping: 30 }}
                    >
                        <button
                            onClick={handleCancelDelete}
                            className="flex h-10 w-10 items-center justify-center rounded-xl bg-destructive-foreground/20 text-destructive-foreground transition-all hover:bg-destructive-foreground/30 active:scale-95"
                        >
                            <X className="h-5 w-5" />
                        </button>
                        <button
                            onClick={handleConfirmDelete}
                            disabled={deleteAPIKey.isPending}
                            className="flex-1 h-10 flex items-center justify-center gap-2 rounded-xl bg-destructive-foreground text-destructive font-semibold transition-all hover:bg-destructive-foreground/90 active:scale-[0.98] disabled:opacity-50 disabled:cursor-not-allowed"
                        >
                            <Trash2 className="h-4 w-4" />
                            {deleteAPIKey.isPending ? '删除中...' : '确认删除'}
                        </button>
                    </motion.div>
                )}
            </AnimatePresence>
        </div>
    );
}
