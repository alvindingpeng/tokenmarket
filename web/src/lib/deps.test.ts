import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

// 依赖清单守卫: package.json 里声明却没人 import 的依赖会一直躺着。
// 实际发生过两次(date-fns 全站零引用、radix-ui 伞包被单包取代), 都靠人工审计才发现。
// 这里把「声明的依赖必须真的被 import」变成一条会失败的测试。
function walk(dir: string, out: string[] = []): string[] {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
        const full = path.join(dir, entry.name);
        if (entry.isDirectory()) walk(full, out);
        else if (/\.(ts|tsx)$/.test(entry.name) && !entry.name.endsWith('.test.ts')) out.push(full);
    }
    return out;
}

function importedModules(files: string[]): Set<string> {
    const modules = new Set<string>();
    const patterns = [/from\s+['"]([^'"]+)['"]/g, /import\(\s*['"]([^'"]+)['"]\s*\)/g, /require\(\s*['"]([^'"]+)['"]\s*\)/g];
    for (const file of files) {
        const source = fs.readFileSync(file, 'utf8');
        for (const pattern of patterns) {
            for (const match of source.matchAll(pattern)) modules.add(match[1]);
        }
    }
    return modules;
}

describe('dependencies', () => {
    it('声明的每个运行时依赖都真的被 import', () => {
        const root = path.resolve(import.meta.dirname, '../..');
        const pkg = JSON.parse(fs.readFileSync(path.join(root, 'package.json'), 'utf8')) as {
            dependencies?: Record<string, string>;
        };
        const modules = importedModules(walk(path.join(root, 'src')));
        const unused = Object.keys(pkg.dependencies ?? {}).filter((dep) =>
            ![...modules].some((module) => module === dep || module.startsWith(dep + '/')));
        expect(unused).toEqual([]);
    });
});
