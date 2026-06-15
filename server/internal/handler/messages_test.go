package handler

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tma1-ai/tma1/server/internal/writeq"
)

func TestBuildMessageInsertSQL(t *testing.T) {
	in := int64(12)
	out := int64(34)
	sql := buildMessageInsertSQL(messagePayload{
		SessionID:    "opencode:abc'123",
		MessageType:  "assistant",
		Role:         "assistant",
		Content:      "hello ' world",
		Model:        "anthropic/claude",
		ToolName:     "bash",
		ToolUseID:    "call-1",
		InputTokens:  &in,
		OutputTokens: &out,
	}, 123)
	for _, want := range []string{
		"INSERT INTO tma1_messages",
		"opencode:abc''123",
		"hello '' world",
		"12, 34, NULL",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL missing %q:\n%s", want, sql)
		}
	}
}

func TestHandleMessagesAcceptsValidPayloadWithoutDB(t *testing.T) {
	s := &Server{
		greptimeHTTPPort: 0,
		logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		writeSem:         writeq.New(64),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/messages", strings.NewReader(`{"session_id":"opencode:abc","message_type":"user","role":"user","content":"hi"}`))
	w := httptest.NewRecorder()
	s.handleMessages(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}
