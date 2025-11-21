package model

type SettingKey string

const (
	SettingKeyProxyURL SettingKey = "proxy_url"
)

type Setting struct {
	Key   SettingKey `json:"key" gorm:"primaryKey"`
	Value string     `json:"value" gorm:"not null"`
}

func DefaultSettings() []Setting {
	return []Setting{
		{Key: SettingKeyProxyURL, Value: ""},
	}
}
