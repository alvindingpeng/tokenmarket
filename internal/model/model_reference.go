package model

import "encoding/json"

// ModelReference records a models.dev entry by normalized upstream model ID.
type ModelReference struct {
	Name          string          `json:"-" gorm:"primaryKey;not null"`
	ContextLength int             `json:"context_length,omitempty" gorm:"not null;default:0"`
	MaxOutput     int             `json:"max_output,omitempty" gorm:"not null;default:0"`
	PriceProvider string          `json:"price_provider,omitempty"`
	OfficialPrice json.RawMessage `json:"official_price,omitempty" gorm:"serializer:json"` // USD per million tokens, including published tiers.
}
