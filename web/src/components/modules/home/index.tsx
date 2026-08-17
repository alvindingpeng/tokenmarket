import { Activity } from './activity';
import { Total } from './total';
import { StatsChart } from './chart';
import { Rank } from './rank';

// Home 渲染首页统计正文。
export function Home() {
    return (
        <div className="h-full min-h-0 space-y-6 overflow-y-auto overscroll-contain rounded-t-3xl pb-24 md:pb-4">
            <Total />
            <Activity />
            <StatsChart />
            <Rank />
        </div>
    );
}
