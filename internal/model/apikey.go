package model

type APIKey struct {
	ID     int    `json:"id" gorm:"primaryKey"`
	Name   string `json:"name" gorm:"not null"`
	APIKey string `json:"api_key" gorm:"not null"`
}
