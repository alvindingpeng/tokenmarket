import { Component, type ErrorInfo, type ReactNode } from 'react';
import { useTranslations } from 'use-intl';
import { RotateCw, TriangleAlert } from 'lucide-react';
import { Button } from '@/components/ui/button';

type BoundaryLabels = {
    title: string;
    description: string;
    retry: string;
    reload: string;
};

type BoundaryProps = {
    children: ReactNode;
    labels: BoundaryLabels;
    // resetKey 变化时清掉错误状态: 切页或切换登录视图后应当重新尝试渲染。
    resetKey?: string | number;
    // full 表示整页兜底(最外层), 否则按内容区兜底, 保留外壳导航。
    full?: boolean;
};

type BoundaryState = {
    error: Error | null;
    seenKey?: string | number;
};

// ErrorBoundaryInner 是唯一需要类组件形态的部分: 错误边界只有类组件 API。
class ErrorBoundaryInner extends Component<BoundaryProps, BoundaryState> {
    state: BoundaryState = { error: null, seenKey: this.props.resetKey };

    static getDerivedStateFromError(error: Error): Partial<BoundaryState> {
        return { error };
    }

    static getDerivedStateFromProps(props: BoundaryProps, state: BoundaryState): Partial<BoundaryState> | null {
        if (props.resetKey !== state.seenKey) return { error: null, seenKey: props.resetKey };
        return null;
    }

    componentDidCatch(error: Error, info: ErrorInfo) {
        // 渲染期异常必须留下现场: 控制台是唯一可诊断的出口(前端没有上报通道)。
        console.error('page render failed', error, info.componentStack);
    }

    private retry = () => this.setState({ error: null });

    render() {
        const { error } = this.state;
        if (!error) return this.props.children;

        const { labels, full } = this.props;
        return (
            <div
                role="alert"
                className={
                    full
                        ? 'flex h-dvh w-full flex-col items-center justify-center gap-4 px-6 text-center'
                        : 'flex h-full min-h-0 w-full flex-col items-center justify-center gap-4 px-6 text-center'
                }
            >
                <TriangleAlert aria-hidden="true" className="size-8 text-destructive" />
                <div className="space-y-1">
                    <p className="text-base font-semibold text-card-foreground">{labels.title}</p>
                    <p className="text-sm text-muted-foreground">{labels.description}</p>
                </div>
                <pre className="max-h-32 max-w-xl overflow-auto rounded-xl border border-border bg-muted/40 px-3 py-2 text-left text-[11px] leading-relaxed text-muted-foreground whitespace-pre-wrap">
                    {error.message}
                </pre>
                <div className="flex items-center gap-2">
                    <Button variant="outline" className="rounded-xl" onClick={this.retry}>
                        <RotateCw aria-hidden="true" className="size-4" />
                        {labels.retry}
                    </Button>
                    <Button className="rounded-xl" onClick={() => window.location.reload()}>
                        {labels.reload}
                    </Button>
                </div>
            </div>
        );
    }
}

// ErrorBoundary 兜住子树里的渲染期异常, 避免单点报错把整站刷成白屏。
// 页面级实例按页面切换重置, 整页实例作为最后一道兜底。
export function ErrorBoundary({ children, resetKey, full }: {
    children: ReactNode;
    resetKey?: string | number;
    full?: boolean;
}) {
    const t = useTranslations('errorBoundary');
    return (
        <ErrorBoundaryInner
            resetKey={resetKey}
            full={full}
            labels={{
                title: t('title'),
                description: t('description'),
                retry: t('retry'),
                reload: t('reload'),
            }}
        >
            {children}
        </ErrorBoundaryInner>
    );
}
