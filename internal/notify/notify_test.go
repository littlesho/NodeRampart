// SPDX-License-Identifier: MIT

package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
)

func TestSecretPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(fakeToken()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readSecret(path); err == nil {
		t.Fatal("expected insecure permissions to be rejected")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSecret(path); err != nil {
		t.Fatal(err)
	}
}

func TestTelegramDoesNotExposeResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("secret upstream body"))
	}))
	defer server.Close()
	tg := &Telegram{token: fakeToken(), chatID: "1", endpoint: server.URL, client: server.Client()}
	err := tg.Send(context.Background(), "hello")
	if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), tg.token) {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestTelegramClientRefusesRedirects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(fakeToken()), 0o600); err != nil {
		t.Fatal(err)
	}
	tg, err := NewTelegram(path, "1", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodGet, "https://api.telegram.org", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tg.client.CheckRedirect(request, nil); err == nil {
		t.Fatal("expected redirects to be refused")
	}
}

func fakeToken() string {
	return "123456789:" + strings.Repeat("A", 35)
}

func TestFormatEventEscapesInput(t *testing.T) {
	event := model.Event{IncidentID: "inc_1", ObservedAt: time.Now(), Kind: "ssh<brute>", Severity: model.SeverityHigh, SourceIP: "203.0.113.9", SourceRange: "203.0.113.0/24", Summary: "<b>untrusted</b>"}
	message := FormatEvent("host&name", event)
	if strings.Contains(message, "<brute>") || strings.Contains(message, "<b>untrusted</b>") || !strings.Contains(message, "host&amp;name") || !strings.Contains(message, "Source: 203.0.113.9") {
		t.Fatalf("message was not escaped: %s", message)
	}
}

func TestJoinWithinDoesNotCutHTMLEntities(t *testing.T) {
	message := joinWithin([]string{"<b>safe</b>", strings.Repeat("&amp;", 1000), "tail"}, 100)
	if len(message) > 100 || !strings.HasSuffix(message, "… truncated") || strings.Contains(message, "&amp") {
		t.Fatalf("unsafe bounded message: %q", message)
	}
}
