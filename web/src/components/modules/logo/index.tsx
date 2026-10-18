// Logo 渲染站点标识 -- 编组场: 三条上游股道扇形汇入, 再由主干统一发出。
// 图形用 currentColor 渐变, 因此跟随主题色(浅色/深色模式自动适配)。
export default function Logo({ size = 48 }: { size?: number | string }) {
    return (
        <svg viewBox="0 0 100 100" xmlns="http://www.w3.org/2000/svg" width={size} height={size} className="text-primary">
            <defs>
                <linearGradient id="switchyardLogo" x1="12" y1="50" x2="88" y2="50" gradientUnits="userSpaceOnUse">
                    <stop offset="0%" stopColor="currentColor" />
                    <stop offset="100%" stopColor="currentColor" stopOpacity="0.72" />
                </linearGradient>
            </defs>
            <g stroke="url(#switchyardLogo)" strokeWidth="12" strokeLinecap="round" strokeLinejoin="round" fill="none">
                <path d="M12 16 L54 50" />
                <path d="M12 50 H54" />
                <path d="M12 84 L54 50" />
                <path d="M54 50 H88" />
            </g>
        </svg>
    );
}