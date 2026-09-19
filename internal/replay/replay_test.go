// SPDX-License-Identifier: MIT

package replay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	no := false
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	if err := encoder.Encode(Header{Format: Format, Version: Version, Anonymized: &no, InterfaceLimit: 2}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2025, 3, 4, 5, 6, 7, 8000, time.UTC)
	batch := protocol.Batch{ProtocolVersion: protocol.Version, SentAt: at, IntervalMillis: 1000, Interface: "private-link", RXPackets: 20, RXBytes: 1200, InboundSYN: 20, Flows: []protocol.Flow{{Direction: model.DirectionInbound, RemoteIP: "192.0.2.77", Protocol: "tcp", LocalPort: 443, RemotePort: 3456, TCPFlags: 2, Packets: 20, Bytes: 1200}}}
	if err := encoder.Encode(Record{Type: "batch", Generation: 5, Batch: &batch}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		a := AuthRecord{ObservedAt: at.Add(time.Duration(i) * 100 * time.Millisecond), Kind: collector.AuthFailure, SourceIP: "192.0.2.77", SourcePort: 3456, User: "private-user", Method: "password"}
		if i == 3 {
			a.Kind = collector.AuthSuccess
			a.Root = true
			a.User = "root"
		}
		if err := encoder.Encode(Record{Type: "auth", Auth: &a}); err != nil {
			t.Fatal(err)
		}
	}
	batch.Flows = nil
	batch.RXPackets = 0
	batch.RXBytes = 0
	batch.InboundSYN = 0
	for i := 1; i <= 2; i++ {
		batch.SentAt = at.Add(time.Duration(i) * time.Second)
		if err := encoder.Encode(Record{Type: "batch", Generation: 5, Batch: &batch}); err != nil {
			t.Fatal(err)
		}
	}
	batch.Interface = "private-v6"
	batch.SentAt = at.Add(3 * time.Second)
	batch.RXPackets = 1
	batch.RXBytes = 60
	batch.Flows = []protocol.Flow{{Direction: model.DirectionInbound, RemoteIP: "2001:db8:abcd::99", Protocol: "tcp", LocalPort: 22, RemotePort: 3456, TCPFlags: 16, Packets: 1, Bytes: 60}}
	if err := encoder.Encode(Record{Type: "batch", Generation: 5, Batch: &batch}); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func defaultRules() Rules {
	cfg := config.Defaults()
	cfg.Auth.Journalctl = ""
	return Rules{NetworkEnabled: true, Detection: cfg.Detection, Auth: cfg.Auth}
}

func TestAnonymizePreservesDetectionsAndComparisonIsReproducible(t *testing.T) {
	var output bytes.Buffer
	info, err := anonymize(context.Background(), bytes.NewReader(fixture(t)), &output)
	if err != nil || info.Input.Batches != 4 || info.Input.AuthObservations != 3 {
		t.Fatalf("anonymize=%+v %v", info, err)
	}
	for _, secret := range []string{"192.0.2.77", "2001:db8:abcd::99", "private-link", "private-user", "private-v6", "2025-03-04", "\"user\":\"root\""} {
		if bytes.Contains(output.Bytes(), []byte(secret)) {
			t.Fatal("original identity survived anonymization")
		}
	}
	baseline, candidate := defaultRules(), defaultRules()
	baseline.Detection.SYNPacketsPerSecond = 100
	baseline.Auth.Threshold = 5
	candidate.Detection.SYNPacketsPerSecond = 10
	candidate.Detection.RecoveryWindows = 2
	candidate.Auth.Threshold = 2
	first, err := compare(context.Background(), bytes.NewReader(output.Bytes()), baseline, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if first.Baseline.TotalEvents != 1 || first.Candidate.TotalEvents != 4 || first.DeltaByKindPhase["syn_flood/start"] != 1 || first.DeltaByKindPhase["syn_flood/recovery"] != 1 || first.DeltaByKindPhase["ssh_brute_force/start"] != 1 {
		t.Fatalf("production rules not preserved: %+v", first)
	}
	second, err := compare(context.Background(), bytes.NewReader(output.Bytes()), baseline, candidate)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if !bytes.Equal(a, b) {
		t.Fatal("random IDs or map order changed comparison")
	}
	var start, recovered string
	for _, event := range first.Candidate.Events {
		if event.Kind == "syn_flood" && event.Phase == "start" {
			start = event.IncidentID
			if !event.At.Equal(epoch) {
				t.Fatal("time epoch not shifted")
			}
		}
		if event.Kind == "syn_flood" && event.Phase == "recovery" {
			recovered = event.IncidentID
		}
		if event.Kind == "ssh_login_success" && event.Severity != model.SeverityMedium {
			t.Fatal("root metadata lost")
		}
	}
	if start == "" || start != recovered {
		t.Fatal("incident linkage lost")
	}
	if _, err := compare(context.Background(), bytes.NewReader(fixture(t)), baseline, candidate); err == nil {
		t.Fatal("comparison accepted original identities")
	}
}

func TestReplayRejectsMalformedUnorderedAndOutOfBoundsMetadata(t *testing.T) {
	raw := fixture(t)
	cases := [][]byte{
		bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":2`), 1),
		bytes.Replace(raw, []byte(`"interface_limit":2`), []byte(`"interface_limit":1`), 1),
		bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"Version":1`), 1),
		bytes.Replace(raw, []byte(`"generation":5`), []byte(`"generation":0`), 1),
		bytes.Replace(raw, []byte(`"rx_packets":20`), []byte(`"rx_packets":0`), 1),
		bytes.Replace(raw, []byte(`"type":"auth"`), []byte(`"type":"auth","raw_journal":"synthetic-secret"`), 1),
		bytes.Replace(raw, []byte(`2025-03-04T05:06:08.000008Z`), []byte(`2025-03-03T05:06:08.000008Z`), 1),
		bytes.Replace(raw, []byte(`2025-03-04T05:06:10.000008Z`), []byte(`2025-03-12T05:06:10.000008Z`), 1),
		append(append([]byte{}, raw...), bytes.Repeat([]byte("x"), MaxRecordBytes+1)...),
	}
	for i, input := range cases {
		_, err := anonymize(context.Background(), bytes.NewReader(input), io.Discard)
		if err == nil {
			t.Fatalf("invalid fixture %d accepted", i)
		}
		if strings.Contains(err.Error(), "synthetic-secret") || strings.Contains(err.Error(), "192.0.2") {
			t.Fatal("error leaked input")
		}
	}
	b := protocol.Batch{ProtocolVersion: protocol.Version, SentAt: epoch, IntervalMillis: 1000, Interface: "if1"}
	if b.Validate() == nil || b.ValidateAt(epoch) != nil {
		t.Fatal("offline clock weakened live skew checks")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := anonymize(ctx, bytes.NewReader(raw), io.Discard); err == nil {
		t.Fatal("cancelled replay proceeded")
	}
}

func TestReplayFilesRejectSymlinksExistingTargetsAndPartialPublication(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.jsonl")
	output := filepath.Join(dir, "output.jsonl")
	if err := os.WriteFile(input, fixture(t), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Anonymize(context.Background(), input, output); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(output)
	if err != nil || stat.Mode().Perm() != 0o600 {
		t.Fatal("unsafe output mode")
	}
	if _, err := Anonymize(context.Background(), input, output); err == nil {
		t.Fatal("existing output replaced")
	}
	if after, _ := os.ReadFile(output); !bytes.Equal(before, after) {
		t.Fatal("existing output changed")
	}
	symlink := filepath.Join(dir, "symlink")
	if err := os.Symlink(input, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := Anonymize(context.Background(), symlink, filepath.Join(dir, "never")); err == nil {
		t.Fatal("input symlink followed")
	}
	parent := filepath.Join(dir, "parent-link")
	if err := os.Symlink(dir, parent); err != nil {
		t.Fatal(err)
	}
	if _, err := Anonymize(context.Background(), input, filepath.Join(parent, "never")); err == nil {
		t.Fatal("parent symlink followed")
	}
	if err := os.WriteFile(input, append(fixture(t), []byte("invalid\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "partial.jsonl")
	if _, err := Anonymize(context.Background(), input, bad); err == nil {
		t.Fatal("partial file published")
	}
	if _, err := os.Lstat(bad); !os.IsNotExist(err) {
		t.Fatal("failed output left final file")
	}
	if err := os.Truncate(input, MaxBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := Anonymize(context.Background(), input, bad); err == nil {
		t.Fatal("oversized file opened")
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ".noderampart-replay-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatal("temporary output leaked")
	}
}

func TestReplayIdentityAndSummaryCaps(t *testing.T) {
	var raw bytes.Buffer
	no := false
	encoder := json.NewEncoder(&raw)
	_ = encoder.Encode(Header{Format: Format, Version: Version, Anonymized: &no, InterfaceLimit: 1})
	for i := 1; i <= MaxEvents+5; i++ {
		a := AuthRecord{ObservedAt: epoch.Add(time.Duration(i) * time.Millisecond), Kind: collector.AuthSuccess, SourceIP: "192.0.2.1", User: "synthetic", Method: "publickey"}
		if err := encoder.Encode(Record{Type: "auth", Auth: &a}); err != nil {
			t.Fatal(err)
		}
	}
	var anon bytes.Buffer
	if _, err := anonymize(context.Background(), &raw, &anon); err != nil {
		t.Fatal(err)
	}
	result, err := compare(context.Background(), &anon, defaultRules(), defaultRules())
	if err != nil || !result.Baseline.Truncated || len(result.Baseline.Events) != MaxEvents || result.Baseline.TotalEvents != MaxEvents+5 {
		t.Fatalf("summary cap failed: %v", err)
	}
	for _, id := range []int{0, 1, MaxIdentities, MaxIdentities + 1} {
		value := fmt.Sprintf("198.%d.%d.%d", 18+(id>>16), byte(id>>8), byte(id))
		if syntheticAddress(value) != (id > 0 && id <= MaxIdentities) {
			t.Fatal("synthetic identity bounds failed")
		}
	}
}
