package model

import (
	"math"
	"strings"
	"testing"
)

// TestLLMPriceValidateRejectsNegative 负价必须被拒。
//
// LLMPrice.Cost 是纯乘法再求和, 没有任何下界钳制:
//
//	cost = (read*Input + output*Output + ...) / 1e6
//
// 于是一个负的单价会让费用变成负数 —— "调用反而加钱"。上游若把负价当作
// 免费额度使用, 等价于无上限充值。此前文本模型只校验了名称非空,
// 负价可以直接落库(媒体模型早有 MediaPrice.Validate, 两处口径不一致)。
func TestLLMPriceValidateRejectsNegative(t *testing.T) {
	cases := []struct {
		name  string
		price LLMPrice
	}{
		{"negative input", LLMPrice{Input: -1}},
		{"negative output", LLMPrice{Output: -0.5}},
		{"negative cache read", LLMPrice{CacheRead: -3}},
		{"negative cache write", LLMPrice{CacheWrite: -0.01}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			err := item.price.Validate()
			if err == nil {
				t.Fatalf("负价被放行: %#v", item.price)
			}
			if !strings.Contains(err.Error(), "must not be negative") {
				t.Fatalf("拒绝原因不可读: %v", err)
			}
		})
	}
}

// TestLLMPriceValidateRejectsNonFinite NaN/Inf 会让费用变成 NaN, 之后所有余额运算都被污染。
func TestLLMPriceValidateRejectsNonFinite(t *testing.T) {
	for _, price := range []LLMPrice{
		{Input: math.NaN()},
		{Output: math.Inf(1)},
		{CacheRead: math.Inf(-1)},
	} {
		if err := price.Validate(); err == nil {
			t.Fatalf("非有限值被放行: %#v", price)
		}
	}
}

// TestLLMPriceValidateRejectsAbsurdMagnitude 量级离谱的单价要拦下。
//
// 典型事故是把"每千 token 价"或"每次调用价"填进"每百万 token"的框里,
// 差 1000 倍; 按 token 计费后会放大成用户余额的瞬间清零。
func TestLLMPriceValidateRejectsAbsurdMagnitude(t *testing.T) {
	if err := (LLMPrice{Input: MaxLLMPricePerMillion + 1}).Validate(); err == nil {
		t.Fatal("超过量级上限的单价被放行")
	}
	if err := (LLMPrice{Input: MaxLLMPricePerMillion}).Validate(); err != nil {
		t.Fatalf("上限本身应当被接受(闭区间): %v", err)
	}
}

// TestLLMPriceValidateAcceptsRealPrices 真实价格必须全部通过 —— 别把正常配置也拦了。
func TestLLMPriceValidateAcceptsRealPrices(t *testing.T) {
	valid := []LLMPrice{
		{},                                      // 全 0: 免费模型是合法配置。
		{Input: 0.15, Output: 0.6},              // gpt-4o-mini 量级。
		{Input: 15, Output: 75, CacheRead: 1.5}, // gpt-4o 量级。
		{Input: 0.0001, CacheWrite: 0.0002},     // 极小但非零。
	}
	for _, price := range valid {
		if err := price.Validate(); err != nil {
			t.Fatalf("合法价格被拒: %#v → %v", price, err)
		}
	}
}
