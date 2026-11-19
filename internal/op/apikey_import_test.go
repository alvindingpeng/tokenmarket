package op

import (
	"context"
	"github.com/alvindingpeng/switchyard/internal/model"
	"testing"
)

func TestImportAPIKeyRejectsAmbiguousOwnershipBeforeWrites(t *testing.T) {
	apiKeyTestDB(t)
	key := model.APIKey{Name: "existing", UserID: 8}
	if err := APIKeyCreate(&key, context.Background()); err != nil {
		t.Fatal(err)
	}
	for name, keys := range map[string][]model.APIKey{
		"other ID same credential":   {{ID: 99, UserID: 9, KeyHash: key.KeyHash}},
		"same ID other owner":        {{ID: key.ID, UserID: 9, KeyHash: key.KeyHash}},
		"same ID other hash":         {{ID: key.ID, UserID: 8, KeyHash: model.APIKeyFingerprint("different")}},
		"duplicate dump fingerprint": {{ID: 100, KeyHash: model.APIKeyFingerprint("duplicate")}, {ID: 101, KeyHash: model.APIKeyFingerprint("duplicate")}},
		"malformed hash":             {{ID: 102, KeyHash: "not-a-fingerprint"}},
		"plaintext conflict":         {{ID: 103, KeyHash: key.KeyHash, APIKey: "different"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DBImportIncremental(context.Background(), &model.DBDump{Version: 6, APIKeys: keys})
			if err == nil {
				t.Fatal("ambiguous credentials accepted")
			}
		})
	}
	if _, err := DBImportIncremental(context.Background(), &model.DBDump{Version: 6, APIKeys: []model.APIKey{{ID: key.ID, UserID: key.UserID, KeyHash: key.KeyHash}}}); err != nil {
		t.Fatal("idempotent import failed", err)
	}
}
