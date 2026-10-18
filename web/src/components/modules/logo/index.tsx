// Logo 渲染站点标识 -- 编组场: 两条上游股道并入一条主干, 再由主干统一发出。
// 图形用 currentColor 渐变, 因此跟随主题色(浅色/深色模式自动适配)。
export default function Logo({ size = 48 }: { size?: number | string }) {
    return (
        <svg viewBox="0 0 100 100" xmlns="http://www.w3.org/2000/svg" width={size} height={size} className="text-primary">
            <defs>
                <linearGradient id="switchyardLogo" x1="26" y1="12" x2="74" y2="88" gradientUnits="userSpaceOnUse">
                    <stop offset="0%" stopColor="currentColor" />
                    <stop offset="100%" stopColor="currentColor" stopOpacity="0.72" />
                </linearGradient>
            </defs>
            <g stroke="url(#switchyardLogo)" strokeWidth="13" strokeLinecap="round" strokeLinejoin="round" fill="none">
                <path d="M30 12 V40 L50 58 V88" />
                <path d="M70 12 V40 L50 58" />
            </g>
        </svg>
    );
}