import { describe, expect, it } from 'vitest';
import { serviceWorkerURL } from './sw-url';

describe('serviceWorkerURL', () => {
    it('把应用版本拼进注册地址', () => {
        expect(serviceWorkerURL('v1.5.39')).toBe('./sw.js?v=v1.5.39');
    });

    it('版本号做 URL 编码, 不产生非法字符', () => {
        expect(serviceWorkerURL('v1.5.39+build.7')).toBe('./sw.js?v=v1.5.39%2Bbuild.7');
    });

    it('空版本回退到固定标记, 不拼出会频繁变动的地址', () => {
        expect(serviceWorkerURL(undefined)).toBe('./sw.js?v=dev');
        expect(serviceWorkerURL('')).toBe('./sw.js?v=dev');
        expect(serviceWorkerURL('   ')).toBe('./sw.js?v=dev');
    });

    it('每个版本都产出不同的地址, 否则浏览器不会重新安装 worker', () => {
        expect(serviceWorkerURL('v1.5.39')).not.toBe(serviceWorkerURL('v1.5.40'));
    });
});
