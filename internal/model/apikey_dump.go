package model

import "encoding/json"

// api_keys 在备份 dump 里的行形状, 只服务于 DBDump 的 JSON 编解码。
//
// 为什么要和 model.APIKey 分开: 备份必须带走指纹(恢复后 Key 才仍然可用), 而 API 响应
// 绝不能带指纹 —— 同一个 struct 没法按场景区分两种 JSON, 故 APIKey.KeyHash 是 json:"-",
// 指纹只经这里进出备份。明文则相反: 老备份(改哈希前导出的)里存的是明文, 导入时原样带进
// model.APIKey.APIKey, 由模型 hook 换算成指纹落库; 新备份一律只写指纹, 不再有明文副本。
type apiKeyDumpRow struct {
	ID                int      `json:"id"`
	UserID            uint     `json:"user_id"`
	Name              string   `json:"name"`
	APIKey            string   `json:"api_key,omitempty"` // 老备份的明文, 导入换算后即丢弃。
	KeyHash           string   `json:"key_hash,omitempty"`
	KeyPrefix         string   `json:"key_prefix,omitempty"`
	KeyLast4          string   `json:"key_last4,omitempty"`
	Enabled           bool     `json:"enabled"`
	ExpireAt          int64    `json:"expire_at,omitempty"`
	MaxCost           float64  `json:"max_cost,omitempty"`
	SupportedModels   []string `json:"supported_models"`
	RoutingPreference string   `json:"routing_preference"`
	QuotaRequests     int64    `json:"quota_requests"`
	QuotaInputTokens  int64    `json:"quota_input_tokens"`
	QuotaOutputTokens int64    `json:"quota_output_tokens"`
	QuotaResetHour    int      `json:"quota_reset_hour"`
}

func apiKeyToDumpRow(k APIKey) apiKeyDumpRow {
	return apiKeyDumpRow{
		ID:                k.ID,
		UserID:            k.UserID,
		Name:              k.Name,
		KeyHash:           k.KeyHash,
		KeyPrefix:         k.KeyPrefix,
		KeyLast4:          k.KeyLast4,
		Enabled:           k.Enabled,
		ExpireAt:          k.ExpireAt,
		MaxCost:           k.MaxCost,
		SupportedModels:   k.SupportedModels,
		RoutingPreference: k.RoutingPreference,
		QuotaRequests:     k.QuotaRequests,
		QuotaInputTokens:  k.QuotaInputTokens,
		QuotaOutputTokens: k.QuotaOutputTokens,
		QuotaResetHour:    k.QuotaResetHour,
	}
}

func apiKeyFromDumpRow(r apiKeyDumpRow) APIKey {
	return APIKey{
		ID:                r.ID,
		UserID:            r.UserID,
		Name:              r.Name,
		APIKey:            r.APIKey,
		KeyHash:           r.KeyHash,
		KeyPrefix:         r.KeyPrefix,
		KeyLast4:          r.KeyLast4,
		Enabled:           r.Enabled,
		ExpireAt:          r.ExpireAt,
		MaxCost:           r.MaxCost,
		SupportedModels:   r.SupportedModels,
		RoutingPreference: r.RoutingPreference,
		QuotaRequests:     r.QuotaRequests,
		QuotaInputTokens:  r.QuotaInputTokens,
		QuotaOutputTokens: r.QuotaOutputTokens,
		QuotaResetHour:    r.QuotaResetHour,
	}
}

// MarshalJSON 导出 dump: api_keys 换成备份行形状 —— 带指纹, 绝不带明文。
// 其余字段(含以后新增的)照常按原样导出, 只有 api_keys 被替换。
func (d DBDump) MarshalJSON() ([]byte, error) {
	type dbDumpAlias DBDump // 剥掉方法, 避免递归回到本函数。
	out := struct {
		dbDumpAlias
		APIKeys []apiKeyDumpRow `json:"api_keys,omitempty"`
	}{dbDumpAlias: dbDumpAlias(d)}
	if len(d.APIKeys) > 0 {
		out.APIKeys = make([]apiKeyDumpRow, 0, len(d.APIKeys))
		for _, k := range d.APIKeys {
			out.APIKeys = append(out.APIKeys, apiKeyToDumpRow(k))
		}
	}
	return json.Marshal(out)
}

// UnmarshalJSON 导入 dump: api_keys 接受两种形状 —— 老备份的明文与新备份的指纹。
// 明文只进内存字段, 由模型 hook 在落库时换算成指纹, 幂等且不会写回明文。
func (d *DBDump) UnmarshalJSON(b []byte) error {
	type dbDumpAlias DBDump
	aux := struct {
		*dbDumpAlias
		APIKeys []apiKeyDumpRow `json:"api_keys,omitempty"`
	}{dbDumpAlias: (*dbDumpAlias)(d)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	if aux.APIKeys == nil {
		return nil
	}
	keys := make([]APIKey, 0, len(aux.APIKeys))
	for _, row := range aux.APIKeys {
		keys = append(keys, apiKeyFromDumpRow(row))
	}
	d.APIKeys = keys
	return nil
}
