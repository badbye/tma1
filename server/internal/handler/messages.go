package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tma1-ai/tma1/server/internal/strutil"
)

const (
	maxMessageBody    = 1 << 20 // 1 MB
	maxMessageContent = 8192
)

type messagePayload struct {
	SessionID           string `json:"session_id"`
	MessageType         string `json:"message_type"`
	Role                string `json:"role"`
	Content             string `json:"content"`
	Model               string `json:"model"`
	ToolName            string `json:"tool_name"`
	ToolUseID           string `json:"tool_use_id"`
	InputTokens         *int64 `json:"input_tokens"`
	OutputTokens        *int64 `json:"output_tokens"`
	CacheReadTokens     *int64 `json:"cache_read_tokens"`
	CacheCreationTokens *int64 `json:"cache_creation_tokens"`
	ReasoningTokens     *int64 `json:"reasoning_tokens"`
	DurationMS          *int64 `json:"duration_ms"`
}

// handleMessages receives normalized conversation-replay rows from adapters
// that do not have a transcript watcher. It deliberately writes only
// tma1_messages; lifecycle/tool events still go through /api/hooks so anomaly
// and peer-session queries share one canonical event path.
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	body, err := io.ReadAll(io.LimitReader(r.Body, maxMessageBody))
	if err != nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	var payload messagePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	if payload.SessionID == "" || payload.MessageType == "" || payload.Role == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if s.greptimeHTTPPort > 0 {
		if !s.writeSem.Go(func() { s.insertMessage(payload) }) {
			s.logger.Warn("write semaphore full, dropping message insert",
				"type", payload.MessageType,
				"dropped_total", s.writeSem.Dropped())
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) insertMessage(p messagePayload) {
	sql := buildMessageInsertSQL(p, time.Now().UnixMilli())
	sqlURL := fmt.Sprintf("http://localhost:%d/v1/sql", s.greptimeHTTPPort)
	form := url.Values{}
	form.Set("sql", sql)

	resp, err := s.httpClient.Post(sqlURL, "application/x-www-form-urlencoded", strings.NewReader(form.Encode())) //nolint:gosec
	if err != nil {
		s.logger.Debug("message insert failed", "error", err, "type", p.MessageType)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode != http.StatusOK {
		s.logger.Debug("message insert non-200", "status", resp.StatusCode, "type", p.MessageType)
		return
	}
	if err := greptimeResponseError(body); err != nil {
		s.logger.Debug("message insert failed", "error", err, "type", p.MessageType)
	}
}

func buildMessageInsertSQL(p messagePayload, ts int64) string {
	return fmt.Sprintf(
		"INSERT INTO tma1_messages (ts, session_id, message_type, \"role\", content, model, tool_name, tool_use_id, "+
			"input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, reasoning_tokens, duration_ms) "+
			"VALUES (%d, '%s', '%s', '%s', '%s', '%s', '%s', '%s', %s, %s, %s, %s, %s, %s)",
		ts,
		escapeSQLString(p.SessionID),
		escapeSQLString(strutil.SafeTruncate(p.MessageType, 64)),
		escapeSQLString(strutil.SafeTruncate(p.Role, 64)),
		escapeSQLString(strutil.SafeTruncate(p.Content, maxMessageContent)),
		escapeSQLString(strutil.SafeTruncate(p.Model, 256)),
		escapeSQLString(strutil.SafeTruncate(p.ToolName, 256)),
		escapeSQLString(p.ToolUseID),
		nullableInt64(p.InputTokens),
		nullableInt64(p.OutputTokens),
		nullableInt64(p.CacheReadTokens),
		nullableInt64(p.CacheCreationTokens),
		nullableInt64(p.ReasoningTokens),
		nullableInt64(p.DurationMS),
	)
}

func nullableInt64(v *int64) string {
	if v == nil {
		return "NULL"
	}
	return fmt.Sprintf("%d", *v)
}
