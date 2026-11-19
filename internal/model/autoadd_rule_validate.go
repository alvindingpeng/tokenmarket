package model

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/dlclark/regexp2"
)

// 模型级条件的校验错误: 文案里带上字段名, 让接口与界面都能直接指出是哪一项填错了。
// 条件填错不能静默回落全局 —— 那等于用户设的条件悄悄不管用, 没有任何提示。
var (
	errAutoAddRuleName = fmt.Errorf("model name is required")
	errAutoAddRuleMode = fmt.Errorf("mode must be on or off")
)

// Normalize 规范化一条模型级条件: 名称小写去空白, 数值与三态字段去空白并校验。
// 返回错误时调用方必须拒绝保存 —— 条件写错却静默回落全局等于设置形同虚设。
func (r *ModelAutoAddRule) Normalize() error {
	r.Name = lowerTrim(r.Name)
	if r.Name == "" {
		return errAutoAddRuleName
	}
	r.Mode = lowerTrim(r.Mode)
	if r.Mode != "" && r.Mode != AutoAddRuleModeOn && r.Mode != AutoAddRuleModeOff {
		return errAutoAddRuleMode
	}
	var err error
	if r.PriceMin, err = numberField(r.PriceMin, "price_min"); err != nil {
		return err
	}
	if r.PriceMax, err = numberField(r.PriceMax, "price_max"); err != nil {
		return err
	}
	if r.MarkupMin, err = numberField(r.MarkupMin, "markup_min"); err != nil {
		return err
	}
	if r.MarkupMax, err = numberField(r.MarkupMax, "markup_max"); err != nil {
		return err
	}
	if r.Exclude, err = regexField(r.Exclude, "exclude"); err != nil {
		return err
	}
	if r.Include, err = regexField(r.Include, "include"); err != nil {
		return err
	}
	r.FillPrice = lowerTrim(r.FillPrice)
	if r.FillPrice != "" && r.FillPrice != AutoAddRuleModeOn && r.FillPrice != AutoAddRuleModeOff {
		return errAutoAddRuleMode
	}
	return nil
}

// Empty 判断规则是否已无任何实际内容: 全空(仅剩归属与模型名)时没有存在意义,
// 保存入口据此删行而不是留一条全默认的死数据。
func (r ModelAutoAddRule) Empty() bool {
	return r.Mode == "" && r.PriceMin == "" && r.PriceMax == "" &&
		r.MarkupMin == "" && r.MarkupMax == "" &&
		r.Exclude == "" && r.Include == "" && r.FillPrice == ""
}

// lowerTrim 小写去空白: 模型名与三态值统一走小写比较, 上游返回的模型名大小写不稳定。
func lowerTrim(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// numberField 去空白并校验是合法且有限的数字; 空串表示不设置, 直接放行。
func numberField(raw, field string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return "", fmt.Errorf("%s must be a number", field)
	}
	return raw, nil
}

// regexField 去空白并校验正则可编译(与全局条件同用 ECMAScript 方言)。
func regexField(raw, field string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if _, err := regexp2.Compile(raw, regexp2.ECMAScript); err != nil {
		return "", fmt.Errorf("%s: invalid regex: %v", field, err)
	}
	return raw, nil
}
