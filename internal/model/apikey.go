package model

type APIKey struct {
	ID     int    `json:"id" gorm:"primaryKey"`
	APIKey string `json:"apiKey" gorm:"not null"`
}
