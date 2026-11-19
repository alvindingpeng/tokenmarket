// 轮询间隔常量。
// 此前 21 处 refetchInterval 直接写裸数字(5000/10000/30000/3600000), 既看不出「为什么是这个数」,
// 也没法一次性调整全站节奏。按「多快需要看到变化」分档, 新增查询时对号入座即可。
// 注意: @tanstack/react-query 默认在页面不可见时暂停轮询, 这些间隔只在标签页可见时生效。

/** 秒级: 限流/探测这类本身按秒刷新的面板。 */
export const POLL_FAST = 5000;
/** 实时: 调用量、余额这类看板指标。 */
export const POLL_REALTIME = 10000;
/** 常态: 列表与状态类数据。 */
export const POLL_NORMAL = 20000;
/** 慢速: 管理面配置, 改动不频繁。 */
export const POLL_SLOW = 30000;
/** 分钟级: 低频统计。 */
export const POLL_MINUTE = 60000;
/** 小时级: 版本号、价格表这类日级数据。 */
export const POLL_HOUR = 3600000;
/** 正在进行的单次请求详情: 紧跟日志流。 */
export const POLL_STREAM = 1000;
