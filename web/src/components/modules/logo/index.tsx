// Logo 渲染站点标识 -- shareapi 主题: 左侧圆角方块是 API 网关, 右侧两个圆点是共享出去的调用端点,
// 连线表示把接口分发给多个使用方。
export default function Logo({ size = 48 }: { size?: number | string }) {
    return (
        <svg viewBox="0 0 100 100" xmlns="http://www.w3.org/2000/svg" width={size} height={size} className="text-primary">
            <defs>
                <linearGradient id="shareapiLogo" x1="10" y1="18" x2="89" y2="82" gradientUnits="userSpaceOnUse">
                    <stop offset="0%" stopColor="currentColor" />
                    <stop offset="100%" stopColor="currentColor" stopOpacity="0.72" />
                </linearGradient>
            </defs>
            <g fill="url(#shareapiLogo)">
                <rect x="10" y="34" width="32" height="32" rx="11" />
                <circle cx="79" cy="23" r="10" />
                <circle cx="79" cy="77" r="10" />
            </g>
            <g stroke="url(#shareapiLogo)" strokeWidth="8" strokeLinecap="round" fill="none">
                <path d="M42 43 L70.2 27.8" />
                <path d="M42 57 L70.2 72.2" />
            </g>
        </svg>
    );
}
