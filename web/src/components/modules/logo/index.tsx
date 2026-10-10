// Logo 渲染站点标识 -- shareapi 主题: 左侧实心圆角方块代表 API 网关, 右侧两个圆环代表共享出去的调用端点, 连线表示分发。
export default function Logo({ size = 48 }: { size?: number | string }) {
    return (
        <svg viewBox="0 0 100 100" xmlns="http://www.w3.org/2000/svg" width={size} height={size} className="text-primary">
            <defs>
                <linearGradient id="shareapiLogo" x1="11" y1="11" x2="90" y2="89" gradientUnits="userSpaceOnUse">
                    <stop offset="0%" stopColor="currentColor" />
                    <stop offset="100%" stopColor="currentColor" stopOpacity="0.72" />
                </linearGradient>
            </defs>
            <g stroke="url(#shareapiLogo)" strokeWidth="8" strokeLinecap="round" fill="none">
                <path d="M41 43 L65.4 30.8" />
                <path d="M41 57 L65.4 69.2" />
                <circle cx="77" cy="25" r="13" />
                <circle cx="77" cy="75" r="13" />
            </g>
            <rect x="11" y="35" width="30" height="30" rx="10" fill="url(#shareapiLogo)" />
        </svg>
    );
}
