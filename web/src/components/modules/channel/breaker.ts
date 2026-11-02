// 熔断与额度暂停都靠后端下发的"截止时间"表达, 前端只负责如实展示。
// disabledUntil 是后端给的绝对时间, 本地时钟偏差会让它偏一点, 这里只做展示, 不参与任何写回。

// isBroken 判断截止时间是否仍在未来, 即当前仍处于熔断/暂停中。
export const isBroken = (disabledUntil: string | null | undefined) => {
    if (!disabledUntil) return false;
    const until = Date.parse(disabledUntil);
    return Number.isFinite(until) && until > Date.now();
};

// remainText 把截止时间渲染成"还剩多久", 例如 1h2m / 5m / 30s。
export const remainText = (disabledUntil: string | null | undefined) => {
    const until = disabledUntil ? Date.parse(disabledUntil) : NaN;
    if (!Number.isFinite(until)) return '';
    const seconds = Math.max(0, Math.floor((until - Date.now()) / 1000));
    if (seconds >= 3600) return Math.floor(seconds / 3600) + 'h' + Math.floor((seconds % 3600) / 60) + 'm';
    if (seconds >= 60) return Math.floor(seconds / 60) + 'm';
    return seconds + 's';
};
