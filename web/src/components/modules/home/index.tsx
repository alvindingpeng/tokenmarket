import { Activity } from './activity';
import { Total } from './total';
import { StatsChart } from './chart';
import { Rank } from './rank';
export function Home() {
    return (
        <div className="space-y-6">
            <Total />
            <Activity />
            <StatsChart />
            <Rank />
        </div>
    );
}
