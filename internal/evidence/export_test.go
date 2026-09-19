// SPDX-License-Identifier: MIT

package evidence

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureBundle(t *testing.T) Bundle {
	t.Helper()
	b, err := Build(rawFixture())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestZIPManifestModeContentAndNoExternalResources(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "evidence.zip")
	if err := Export(context.Background(), fixtureBundle(t), path, ""); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 || info.Size() > MaxOutputBytes {
		t.Fatal("unsafe published artifact", err)
	}
	z, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	if len(z.File) != 3 {
		t.Fatal("unexpected archive members")
	}
	files := map[string][]byte{}
	for _, f := range z.File {
		if f.Mode().Perm() != 0o600 {
			t.Fatal("unsafe archive member mode")
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = data
	}
	expected := fmt.Sprintf("%x  index.html\n%x  evidence.json\n", sha256.Sum256(files["index.html"]), sha256.Sum256(files["evidence.json"]))
	if string(files["SHA256SUMS"]) != expected {
		t.Fatal("manifest did not cover exact rendered files")
	}
	page := string(files["index.html"])
	for _, forbidden := range []string{"<script", "<iframe", "<img", "<a ", " src=", " href=", "http://", "https://", "private_"} {
		if strings.Contains(page, forbidden) {
			t.Fatalf("HTML contains unexpected resource or raw value %q", forbidden)
		}
	}
	var b Bundle
	if json.Unmarshal(files["evidence.json"], &b) != nil || b.Validate() != nil || !strings.Contains(page, b.Events[0].Alias) {
		t.Fatal("HTML and JSON do not share the same sanitized DTO")
	}
}

func TestEveryExportRekeysWithoutMutatingInput(t *testing.T) {
	b := fixtureBundle(t)
	old := b.Events[0].Alias
	var aliases []string
	for _, name := range []string{"a.json", "b.json"} {
		path := filepath.Join(t.TempDir(), name)
		if err := Export(context.Background(), b, path, "json"); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(path)
		var result Bundle
		if json.Unmarshal(data, &result) != nil {
			t.Fatal("invalid JSON")
		}
		aliases = append(aliases, result.Events[0].Alias)
		if result.Events[0].Alias != result.RelatedSSH[0].Alias || result.Events[0].IncidentAlias != result.Incident.Alias {
			t.Fatal("rekey broke internal references")
		}
	}
	if aliases[0] == aliases[1] || aliases[0] == old || b.Events[0].Alias != old {
		t.Fatal("export reused aliases or mutated input")
	}
}

func TestHTMLTemplateEscapesEvenBeforeDTOValidation(t *testing.T) {
	b := fixtureBundle(t)
	b.Events[0].Kind = `</td><script>alert("synthetic")</script>`
	page, err := renderHTML(context.Background(), b, []byte(`<img src="https://synthetic.invalid">`))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(page, []byte("<script>")) || bytes.Contains(page, []byte("<img ")) || !bytes.Contains(page, []byte("&lt;script&gt;")) {
		t.Fatal("template did not escape untrusted strings")
	}
	path := filepath.Join(t.TempDir(), "bad.html")
	if err := Export(context.Background(), b, path, "html"); err == nil {
		t.Fatal("invalid public DTO reached export")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid DTO created output")
	}
}

func TestExportRefusesCollisionsSymlinksAndCancellation(t *testing.T) {
	dir := t.TempDir()
	b := fixtureBundle(t)
	old := filepath.Join(dir, "old")
	if err := os.WriteFile(old, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(old, link); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(dir, "parent")
	if err := os.Symlink(dir, parent); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{old, link, filepath.Join(parent, "new"), dir + "/../out"} {
		if err := Export(context.Background(), b, path, "json"); err == nil {
			t.Fatal("unsafe path accepted")
		}
	}
	data, _ := os.ReadFile(old)
	if string(data) != "preserve" {
		t.Fatal("existing output replaced")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(dir, "cancelled")
	if err := Export(ctx, b, path, "zip"); err == nil {
		t.Fatal("cancelled export succeeded")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled export published output")
	}
	for _, format := range []string{"html", "json"} {
		if err := Export(context.Background(), b, filepath.Join(dir, "ok."+format), format); err != nil {
			t.Fatal(err)
		}
	}
}

func TestByteLimitsAndInterruptedWriters(t *testing.T) {
	var out bytes.Buffer
	w := &boundedWriter{ctx: context.Background(), out: &out, remaining: 3}
	if _, err := w.Write([]byte("four")); err == nil || out.Len() != 0 {
		t.Fatal("byte bound wrote a partial oversized value")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w = &boundedWriter{ctx: ctx, out: &out, remaining: 10}
	if _, err := w.Write([]byte("x")); err == nil || out.Len() != 0 {
		t.Fatal("cancelled writer continued")
	}
	b := fixtureBundle(t)
	if _, err := renderHTML(context.Background(), b, bytes.Repeat([]byte("<"), MaxHTMLBytes)); err == nil {
		t.Fatal("HTML byte limit not enforced after escaping")
	}
	b.Events = make([]Event, 101)
	if err := b.Validate(); err == nil {
		t.Fatal("oversized public records accepted")
	}
}
