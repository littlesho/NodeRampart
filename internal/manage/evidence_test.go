// SPDX-License-Identifier: MIT

package manage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/evidence"
	"github.com/littlesho/NodeRampart/internal/store"
)

func TestManagementEvidenceAnchorsExplicitWindowAndDoesNotApplyConfig(t *testing.T) {
	m, services := fixtureManager(t)
	before, err := os.ReadFile(m.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	end := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	called := false
	m.Request = func(ctx context.Context, command string, args any) (json.RawMessage, error) {
		q, ok := args.(api.EvidenceArgs)
		if !ok || command != "evidence_snapshot" || !q.End.Equal(end) || !q.Start.Equal(end.Add(-24*time.Hour)) {
			t.Fatal("management evidence query changed")
		}
		called = true
		b, err := evidence.Build(store.EvidenceSnapshot{Start: q.Start, End: q.End, AsOf: end})
		if err != nil {
			t.Fatal(err)
		}
		return json.Marshal(b)
	}
	services.calls = nil
	output := filepath.Join(t.TempDir(), "evidence.json")
	if _, err := m.Action(context.Background(), "evidence_export", map[string]string{"until": end.Format(time.RFC3339), "format": "json", "output": output}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(m.ConfigPath)
	if err != nil || string(before) != string(after) || len(services.calls) != 0 || !called {
		t.Fatal("export changed configuration or services")
	}
	info, err := os.Stat(output)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("private export missing")
	}
}
