package model

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CreemProduct Creem 充值档位: 展示/入账基准金额(分) + 入账余额 + Creem 商品 ID。
type CreemProduct struct {
	ProductID   string  `json:"product_id"`
	Name        string  `json:"name"`
	AmountCents int64   `json:"amount_cents"`
	Credits     float64 `json:"credits"`
}

// ParseCreemProducts 解析档位配置 JSON 数组; 空串返回空表。
// 放在 model 层, 设置校验与支付网关共用同一份口径。
func ParseCreemProducts(value string) ([]CreemProduct, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	products := []CreemProduct{}
	if err := json.Unmarshal([]byte(value), &products); err != nil {
		return nil, fmt.Errorf("creem products must be a JSON array")
	}
	for _, product := range products {
		if strings.TrimSpace(product.ProductID) == "" || product.AmountCents <= 0 || product.Credits <= 0 {
			return nil, fmt.Errorf("creem product needs product_id, positive amount_cents and credits")
		}
	}
	return products, nil
}
