'use client';

import { useState } from 'react';
import { useTranslations } from 'next-intl';
import { KeyRound, Plus, Loader, Copy, Trash2, Check, X } from 'lucide-react';
import { motion, AnimatePresence } from 'motion/react';
import { Input } from '@/components/ui/input';
import { useAPIKeyList, useCreateAPIKey, useDeleteAPIKey, type APIKey } from '@/api/endpoints/apikey';

function KeyItem({ apiKey }: { apiKey: APIKey }) {
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

    return (
        <motion.div
            layout
            initial={{ opacity: 0, scale: 0.95 }}
            animate={{ opacity: 1, scale: 1 }}
            exit={{ opacity: 0, scale: 0.95, transition: { duration: 0.2 } }}
            transition={{ type: 'spring', stiffness: 500, damping: 30 }}
            className="group relative flex items-center justify-between gap-3 p-3 rounded-xl bg-muted/50 overflow-hidden origin-top">
            <span className="text-sm font-medium truncate">{apiKey.name}</span>

            <div className="flex items-center gap-1.5">
                <button
                    onClick={handleCopy}
                    className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary/10 text-primary transition-all hover:bg-primary hover:text-primary-foreground active:scale-95"
                >
                    <AnimatePresence mode="wait">
                        {copied ? (
                            <motion.div
                                key="check"
                                initial={{ scale: 0 }}
                                animate={{ scale: 1 }}
                                exit={{ scale: 0 }}
                            >
                                <Check className="h-4 w-4" />
                            </motion.div>
                        ) : (
                            <motion.div
                                key="copy"
                                initial={{ scale: 0 }}
                                animate={{ scale: 1 }}
                                exit={{ scale: 0 }}
                            >
                                <Copy className="h-4 w-4" />
                            </motion.div>
                        )}
                    </AnimatePresence>
                </button>

                {!confirmDelete && (
                    <motion.button
                        layoutId={`delete-btn-${apiKey.id}`}
                        onClick={() => setConfirmDelete(true)}
                        className="flex h-8 w-8 items-center justify-center rounded-lg bg-destructive/10 text-destructive transition-colors hover:bg-destructive hover:text-destructive-foreground"
                    >
                        <Trash2 className="h-4 w-4" />
                    </motion.button>
                )}
            </div>

            <AnimatePresence>
                {confirmDelete && (
                    <motion.div
                        layoutId={`delete-btn-${apiKey.id}`}
                        className="absolute inset-0 flex items-center justify-center gap-2 bg-destructive p-3 rounded-xl"
                        transition={{ type: 'spring', stiffness: 400, damping: 30 }}
                    >
                        <button
                            onClick={() => setConfirmDelete(false)}
                            className="flex h-8 w-8 items-center justify-center rounded-lg bg-destructive-foreground/20 text-destructive-foreground transition-all hover:bg-destructive-foreground/30 active:scale-95"
                        >
                            <X className="h-4 w-4" />
                        </button>
                        <button
                            onClick={() => deleteAPIKey.mutate(apiKey.id)}
                            disabled={deleteAPIKey.isPending}
                            className="flex-1 h-8 flex items-center justify-center gap-1.5 rounded-lg bg-destructive-foreground text-destructive text-sm font-medium transition-all hover:bg-destructive-foreground/90 active:scale-[0.98] disabled:opacity-50"
                        >
                            <Trash2 className="h-3.5 w-3.5" />
                            {deleteAPIKey.isPending ? '...' : '确认'}
                        </button>
                    </motion.div>
                )}
            </AnimatePresence>
        </motion.div>
    );
}

export function SettingAPIKey() {
    const t = useTranslations('setting');
    const { data: apiKeys } = useAPIKeyList();
    const createAPIKey = useCreateAPIKey();
    const [name, setName] = useState('');

    const handleSubmit = (e: React.FormEvent) => {
        e.preventDefault();
        if (!name.trim()) return;
        createAPIKey.mutate({ name: name.trim() }, {
            onSuccess: () => setName('')
        });
    };

    return (
        <div className="rounded-3xl border border-border bg-card p-6 custom-shadow space-y-5">
            <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                <KeyRound className="h-5 w-5" />
                {t('apiKey.title')}
            </h2>

            {/* 创建表单 */}
            <form onSubmit={handleSubmit} className="flex items-center gap-2">
                <Input
                    type="text"
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    placeholder={t('apiKey.placeholder')}
                    disabled={createAPIKey.isPending}
                    className="flex-1 rounded-xl"
                />
                <button
                    type="submit"
                    disabled={createAPIKey.isPending || !name.trim()}
                    className="flex h-9 w-9 items-center justify-center rounded-xl bg-primary text-primary-foreground transition-all hover:bg-primary/90 active:scale-95 disabled:opacity-50 disabled:cursor-not-allowed"
                >
                    <AnimatePresence mode="wait">
                        {createAPIKey.isPending ? (
                            <motion.div
                                key="loading"
                                initial={{ scale: 0 }}
                                animate={{ scale: 1 }}
                                exit={{ scale: 0 }}
                            >
                                <Loader className="h-4 w-4 animate-spin" />
                            </motion.div>
                        ) : (
                            <motion.div
                                key="plus"
                                initial={{ scale: 0 }}
                                animate={{ scale: 1 }}
                                exit={{ scale: 0 }}
                            >
                                <Plus className="h-4 w-4" />
                            </motion.div>
                        )}
                    </AnimatePresence>
                </button>
            </form>

            {/* API Key 列表 */}
            <div className="space-y-2 h-32 overflow-y-auto">
                {apiKeys?.length === 0 ? (
                    <div className="h-full flex items-center justify-center text-sm text-muted-foreground">
                        {t('apiKey.empty')}
                    </div>
                ) : (
                    <AnimatePresence>
                        {apiKeys?.sort((a, b) => a.id - b.id).map((apiKey) => (
                            <KeyItem key={apiKey.id} apiKey={apiKey} />
                        ))}
                    </AnimatePresence>
                )}
            </div>
        </div>
    );
}

