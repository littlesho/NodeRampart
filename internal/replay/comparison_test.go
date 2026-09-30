// SPDX-License-Identifier: MIT

package replay

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSavedComparisonValidationRemainsBoundedAndStrict(t *testing.T) {
	var anon bytes.Buffer
	if _, err := anonymize(context.Background(), bytes.NewReader(fixture(t)), &anon); err != nil {
		t.Fatal(err)
	}
	comparison, err := compare(context.Background(), bytes.NewReader(anon.Bytes()), defaultRules(), defaultRules())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "comparison.json")
	data, _ := json.Marshal(comparison)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadComparison(path); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{bytes.Replace(data, []byte(`"format_version":1`), []byte(`"format_version":999`), 1), bytes.Replace(data, []byte(`"format_version":1`), []byte(`"format_version":1,"format_version":1`), 1), []byte(strings.Repeat("x", (4<<20)+1))} {
		if err := os.WriteFile(path, bad, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadComparison(path); err == nil {
			t.Fatal("unsafe/future/oversize comparison accepted")
		}
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linked")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadComparison(link); err == nil {
		t.Fatal("linked comparison accepted")
	}
}
