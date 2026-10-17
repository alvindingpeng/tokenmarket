import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import * as polling from './polling';

// 轮询间隔必须走 lib/polling 的常量。
// 裸数字的问题不是「不好看」: 一是看不出这个数字为什么这么取, 二是想统一调整全站节奏时
// 得靠 grep 逐个改, 漏掉的站点会长期以错误的频率打后端。
describe('polling intervals', () => {
    it('api 层不再出现裸数字的 refetchInterval', () => {
        const dir = path.resolve(import.meta.dirname, '../api');
        const offenders: string[] = [];
        for (const file of fs.readdirSync(dir)) {
            if (!file.endsWith('.ts') || file.endsWith('.test.ts')) continue;
            const source = fs.readFileSync(path.join(dir, file), 'utf8');
            for (const match of source.matchAll(/refetchInterval:\s*(\d+)/g)) {
                offenders.push(file + ': ' + match[1]);
            }
        }
        expect(offenders).toEqual([]);
    });

    it('常量由快到慢递增且都是正整数', () => {
        const ordered = ['POLL_STREAM', 'POLL_FAST', 'POLL_REALTIME', 'POLL_NORMAL', 'POLL_SLOW', 'POLL_MINUTE', 'POLL_HOUR'] as const;
        const values = ordered.map((name) => polling[name]);
        for (const value of values) {
            expect(Number.isInteger(value) && value > 0).toBe(true);
        }
        expect(values.slice().sort((a, b) => a - b)).toEqual(values);
    });
});
