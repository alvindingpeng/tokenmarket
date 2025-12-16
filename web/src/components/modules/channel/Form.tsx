import { ChannelType, useFetchModel } from '@/api/endpoints/channel';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Badge } from '@/components/ui/badge';
import { useTranslations } from 'next-intl';
import { useState, useRef, useEffect } from 'react';
import { RefreshCw, X, Search, Plus } from 'lucide-react';

export interface ChannelFormData {
    name: string;
    type: ChannelType;
    base_url: string;
    key: string;
    model: string;
    enabled: boolean;
    proxy: boolean;
}

export interface ChannelFormProps {
    formData: ChannelFormData;
    onFormDataChange: (data: ChannelFormData) => void;
    onSubmit: (event: React.FormEvent<HTMLFormElement>) => void;
    isPending: boolean;
    submitText: string;
    pendingText: string;
    onCancel?: () => void;
    cancelText?: string;
    idPrefix?: string;
}

export function ChannelForm({
    formData,
    onFormDataChange,
    onSubmit,
    isPending,
    submitText,
    pendingText,
    onCancel,
    cancelText,
    idPrefix = 'channel',
}: ChannelFormProps) {
    const t = useTranslations('channel.form');

    const [modelList, setModelList] = useState<string[]>([]);
    const [selectedModels, setSelectedModels] = useState<string[]>(
        formData.model ? formData.model.split(',').filter(m => m.trim()) : []
    );
    const [inputValue, setInputValue] = useState('');
    const inputRef = useRef<HTMLInputElement>(null);

    const fetchModel = useFetchModel();

    // 同步外部 formData.model 变化到 selectedModels
    useEffect(() => {
        const modelsFromProps = formData.model ? formData.model.split(',').filter(m => m.trim()) : [];
        const currentModels = selectedModels.join(',');
        const propsModels = modelsFromProps.join(',');

        if (currentModels !== propsModels) {
            setSelectedModels(modelsFromProps);
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [formData.model]);

    // 更新表单数据
    useEffect(() => {
        const newModelString = selectedModels.join(',');
        if (formData.model !== newModelString) {
            onFormDataChange({ ...formData, model: newModelString });
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [selectedModels]);

    // 刷新模型列表
    const handleRefreshModels = async () => {
        if (!formData.base_url || !formData.key) {
            return;
        }

        fetchModel.mutate(
            {
                name: formData.name,
                type: formData.type,
                base_url: formData.base_url,
                key: formData.key,
                model: formData.model,
                enabled: formData.enabled,
                proxy: formData.proxy,
            },
            {
                onSuccess: (data) => {
                    setModelList(data ?? []);
                },
            }
        );
    };

    // 添加模型
    const handleAddModel = (model: string) => {
        const trimmedModel = model.trim();
        if (trimmedModel && !selectedModels.includes(trimmedModel)) {
            setSelectedModels([...selectedModels, trimmedModel]);
        }
        setInputValue('');
    };

    // 移除模型
    const handleRemoveModel = (model: string) => {
        setSelectedModels(selectedModels.filter(m => m !== model));
    };

    // 处理回车
    const handleInputKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
        if (e.key === 'Enter') {
            e.preventDefault();
            if (inputValue.trim()) {
                handleAddModel(inputValue);
            }
        }
    };

    // 全选模型
    const handleSelectAll = () => {
        const unselectedModels = modelList.filter(model => !selectedModels.includes(model));
        if (unselectedModels.length > 0) {
            setSelectedModels([...selectedModels, ...unselectedModels]);
        }
    };

    return (
        <form onSubmit={onSubmit} className="space-y-5 px-1">
            <div className="space-y-2">
                <label htmlFor={`${idPrefix}-name`} className="text-sm font-medium text-card-foreground">
                    {t('name')}
                </label>
                <Input
                    className='rounded-xl'
                    id={`${idPrefix}-name`}
                    type="text"
                    value={formData.name}
                    onChange={(event) => onFormDataChange({ ...formData, name: event.target.value })}
                    required
                />
            </div>

            <div className="space-y-2">
                <label htmlFor={`${idPrefix}-type`} className="text-sm font-medium text-card-foreground">
                    {t('type')}
                </label>
                <Select
                    value={String(formData.type)}
                    onValueChange={(value) => onFormDataChange({ ...formData, type: Number(value) as ChannelType })}
                >
                    <SelectTrigger id={`${idPrefix}-type`} className="rounded-xl w-full border border-border px-4 py-2 text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent className='rounded-xl'>
                        <SelectItem className='rounded-xl' value={String(ChannelType.OpenAIChat)}>{t('typeOpenAIChat')}</SelectItem>
                        <SelectItem className='rounded-xl' value={String(ChannelType.OpenAIResponse)}>{t('typeOpenAIResponse')}</SelectItem>
                        <SelectItem className='rounded-xl' value={String(ChannelType.Anthropic)}>{t('typeAnthropic')}</SelectItem>
                        <SelectItem className='rounded-xl' value={String(ChannelType.OneAPI)}>{t('typeOneAPI')}</SelectItem>
                    </SelectContent>
                </Select>
            </div>

            <div className="space-y-2">
                <label htmlFor={`${idPrefix}-base`} className="text-sm font-medium text-card-foreground">
                    {t('baseUrl')}
                </label>
                <Input
                    id={`${idPrefix}-base`}
                    type="url"
                    value={formData.base_url}
                    onChange={(event) => onFormDataChange({ ...formData, base_url: event.target.value })}
                    required
                    className='rounded-xl'
                />
            </div>

            <div className="space-y-2">
                <label htmlFor={`${idPrefix}-key`} className="text-sm font-medium text-card-foreground">
                    {t('apiKey')}
                </label>
                <Input
                    id={`${idPrefix}-key`}
                    type="text"
                    value={formData.key}
                    onChange={(event) => onFormDataChange({ ...formData, key: event.target.value })}
                    required
                    className='rounded-xl'
                />
            </div>

            <div className="space-y-3">
                <div className="flex items-center justify-between">
                    <label className="text-sm font-medium text-card-foreground">
                        {t('model')}
                    </label>
                    {modelList.length > 0 && (
                        <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            onClick={handleRefreshModels}
                            disabled={!formData.base_url || !formData.key || fetchModel.isPending}
                            className="h-6 px-2 text-xs text-muted-foreground/50 hover:text-muted-foreground hover:bg-transparent"
                        >
                            <RefreshCw className={`h-3 w-3 mr-1 ${fetchModel.isPending ? 'animate-spin' : ''}`} />
                            {t('modelRefresh')}
                        </Button>
                    )}
                </div>
                <input
                    type="hidden"
                    value={formData.model}
                    required
                />

                {/* 搜索/添加模型输入框 */}
                <div className="relative">
                    <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground pointer-events-none" />
                    <Input
                        ref={inputRef}
                        id={`${idPrefix}-model-custom`}
                        type="text"
                        value={inputValue}
                        onChange={(e) => setInputValue(e.target.value)}
                        onKeyDown={handleInputKeyDown}
                        placeholder={t('modelSearchPlaceholder')}
                        className="pl-9 pr-10 rounded-xl"
                    />
                    {inputValue.trim() && !selectedModels.includes(inputValue.trim()) && (
                        <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            onClick={() => handleAddModel(inputValue)}
                            className="absolute rounded-lg right-1 top-1/2 -translate-y-1/2 h-7 w-7 p-0 text-muted-foreground hover:bg-accent hover:text-accent-foreground transition-colors"
                            title={t('modelAdd')}
                        >
                            <Plus className="h-4 w-4" />
                        </Button>
                    )}
                </div>

                {/* 可选模型列表（带搜索过滤） */}
                {modelList.length > 0 ? (
                    <div className="space-y-1.5">
                        <div className="flex items-center justify-between">
                            <label className="text-xs font-medium text-muted-foreground">
                                {t('modelAvailable')}
                            </label>
                            <Button
                                type="button"
                                variant="ghost"
                                size="sm"
                                onClick={handleSelectAll}
                                disabled={modelList.filter(model => !selectedModels.includes(model)).length === 0}
                                className="h-6 px-2 text-xs text-muted-foreground/50 hover:text-muted-foreground hover:bg-transparent"
                            >
                                {t('modelSelectAll')}
                            </Button>
                        </div>
                        <div className="rounded-xl border border-border bg-muted/30 p-2.5 max-h-36 min-h-12 overflow-y-auto">
                            {(() => {
                                const searchTerm = inputValue.trim().toLowerCase();
                                const filteredModels = modelList
                                    .filter(model => !selectedModels.includes(model))
                                    .filter(model => !searchTerm || model.toLowerCase().includes(searchTerm));

                                if (filteredModels.length > 0) {
                                    return (
                                        <div className="flex flex-wrap gap-1.5">
                                            {filteredModels.map((model) => (
                                                <Badge
                                                    key={model}
                                                    variant="secondary"
                                                    className="cursor-pointer hover:bg-accent hover:text-accent-foreground transition-colors"
                                                    onClick={() => handleAddModel(model)}
                                                >
                                                    {model}
                                                </Badge>
                                            ))}
                                        </div>
                                    );
                                } else if (searchTerm && !selectedModels.includes(inputValue.trim())) {
                                    return (
                                        <div className="flex items-center justify-center h-8 text-xs text-muted-foreground">
                                            <span>{t('modelNoMatch')}</span>
                                            <Button
                                                type="button"
                                                variant="link"
                                                size="sm"
                                                onClick={() => handleAddModel(inputValue)}
                                                className="h-auto p-0 ml-1 text-xs text-primary"
                                            >
                                                {t('modelAddCustom')}
                                            </Button>
                                        </div>
                                    );
                                } else {
                                    return (
                                        <div className="flex items-center justify-center h-8 text-xs text-muted-foreground">
                                            {t('modelAllAdded')}
                                        </div>
                                    );
                                }
                            })()}
                        </div>
                    </div>
                ) : (
                    <button
                        type="button"
                        onClick={handleRefreshModels}
                        disabled={!formData.base_url || !formData.key || fetchModel.isPending}
                        className="w-full rounded-xl border border-dashed border-border bg-muted/20 p-5 transition-colors hover:bg-muted/40 hover:border-solid disabled:cursor-not-allowed disabled:opacity-50 disabled:hover:bg-muted/20"
                    >
                        <div className="flex flex-col items-center justify-center gap-2 text-muted-foreground">
                            <RefreshCw className={`h-5 w-5 ${fetchModel.isPending ? 'animate-spin' : ''}`} />
                            <span className="text-sm">
                                {fetchModel.isPending ? t('modelFetching') : t('modelFetchPrompt')}
                            </span>
                        </div>
                    </button>
                )}

                {/* 已选模型列表 */}
                <div className="space-y-1.5">
                    <label className="text-xs font-medium text-card-foreground">
                        {t('modelSelected')} {selectedModels.length > 0 && `(${selectedModels.length})`}
                    </label>
                    <div className="rounded-xl border border-border bg-muted/30 p-2.5 max-h-32 min-h-12 overflow-y-auto">
                        {selectedModels.length > 0 ? (
                            <div className="flex flex-wrap gap-1.5">
                                {selectedModels.map((model) => (
                                    <Badge key={model} className="bg-primary hover:bg-primary/90">
                                        {model}
                                        <button
                                            type="button"
                                            onClick={() => handleRemoveModel(model)}
                                            className="ml-1 rounded-sm opacity-70 hover:opacity-100 focus:outline-none focus:ring-1 focus:ring-ring"
                                        >
                                            <X className="h-3 w-3" />
                                        </button>
                                    </Badge>
                                ))}
                            </div>
                        ) : (
                            <div className="flex items-center justify-center h-8 text-xs text-muted-foreground">
                                {t('modelNoSelected')}
                            </div>
                        )}
                    </div>
                </div>
            </div>

            <div className="grid gap-4 sm:grid-cols-2">
                <label className="flex items-center gap-2">
                    <Switch
                        checked={formData.enabled}
                        onCheckedChange={(checked) => onFormDataChange({ ...formData, enabled: checked })}
                    />
                    <span className="text-sm text-card-foreground">{t('enabled')}</span>
                </label>

                <label className="flex items-center gap-2">
                    <Switch
                        checked={formData.proxy}
                        onCheckedChange={(checked) => onFormDataChange({ ...formData, proxy: checked })}
                    />
                    <span className="text-sm text-card-foreground">{t('proxy')}</span>
                </label>
            </div>

            <div className={`flex flex-col gap-3 pt-2 ${onCancel ? 'sm:flex-row' : ''}`}>
                {onCancel && cancelText && (
                    <Button
                        type="button"
                        variant="secondary"
                        onClick={onCancel}
                        className="w-full sm:flex-1 rounded-2xl h-12"
                    >
                        {cancelText}
                    </Button>
                )}
                <Button
                    type="submit"
                    disabled={isPending}
                    className="w-full sm:flex-1 rounded-2xl h-12"
                >
                    {isPending ? pendingText : submitText}
                </Button>
            </div>
        </form>
    );
}
