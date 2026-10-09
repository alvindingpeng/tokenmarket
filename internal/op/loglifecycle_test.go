package op

import (
	"strings"
	"testing"
)

func TestMaskJSONBody(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantMask bool
	}{
		{"simple api_key", "{\"api_key\":\"sk-123\",\"model\":\"gpt\"}", true},
		{"nested authorization", "{\"headers\":{\"Authorization\":\"Bearer x\"},\"model\":\"m\"}", true},
		{"array of tokens", "{\"items\":[{\"token\":\"abc\"},{\"token\":\"def\"}]}", true},
		{"password field", "{\"password\":\"pw123456\",\"model\":\"m\"}", true},
		{"non json passthrough", "plain text", false},
		{"case insensitive", "{\"API_KEY\":\"sk-1\",\"MODEL\":\"m\"}", true},
		{"hyphen normalized", "{\"access-token\":\"sk-2\"}", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := MaskJSONBody(tc.input)
			if tc.wantMask {
				if !strings.Contains(result, "***") {
					t.Errorf("expected mask marker in %s", result)
				}
				if strings.Contains(result, "sk-123") || strings.Contains(result, "Bearer x") || strings.Contains(result, "sk-1") || strings.Contains(result, "sk-2") || strings.Contains(result, "pw123456") {
					t.Errorf("masked body still contains sensitive value: %s", result)
				}
			} else {
				if result != tc.input {
					t.Errorf("passthrough expected: got %s", result)
				}
			}
		})
	}
}

func TestShouldStoreBody(t *testing.T) {
	if !ShouldStoreBody() {
		t.Error("default should store body")
	}
}

func TestLogRetentionDaysDefault(t *testing.T) {
	days := LogRetentionDays()
	if days != 7 {
		t.Errorf("default retention should be 7, got %d", days)
	}
}
