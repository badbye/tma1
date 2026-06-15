package hooks

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCodeInstallEndToEndIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := filepath.Join(t.TempDir(), "myproj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}

	inst := &OpenCodeInstaller{
		DataDir:    filepath.Join(home, ".tma1"),
		Port:       14318,
		ProjectDir: project,
		Logger:     slog.Default(),
	}

	rep, err := inst.Install()
	if err != nil {
		t.Fatalf("first install: %v", err)
	}
	if len(rep.Changed) == 0 {
		t.Error("first install should report changes")
	}

	pluginPath := filepath.Join(home, ".config", "opencode", "plugins", "tma1.js")
	plugin := string(mustRead(t, pluginPath))
	if !strings.Contains(plugin, "/api/hooks?source=opencode") {
		t.Error("plugin missing opencode hook endpoint")
	}
	if !strings.Contains(plugin, "/api/messages") {
		t.Error("plugin missing message endpoint")
	}

	configPath := filepath.Join(home, ".config", "opencode", "opencode.json")
	var parsed map[string]any
	if err := json.Unmarshal(mustRead(t, configPath), &parsed); err != nil {
		t.Fatalf("parse opencode.json: %v", err)
	}
	mcp := parsed["mcp"].(map[string]any)
	tma1 := mcp["tma1"].(map[string]any)
	if tma1["type"] != "local" {
		t.Errorf("mcp type = %v, want local", tma1["type"])
	}
	env := tma1["environment"].(map[string]any)
	if env["TMA1_MCP_CALLER"] != "opencode" {
		t.Errorf("TMA1_MCP_CALLER = %v, want opencode", env["TMA1_MCP_CALLER"])
	}

	if !strings.Contains(string(mustRead(t, filepath.Join(project, "AGENTS.md"))), "<!-- tma1:start -->") {
		t.Error("AGENTS.md missing tma1 marker")
	}

	rep2, err := inst.Install()
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if len(rep2.Changed) != 0 {
		t.Errorf("second install should be a no-op, got changes: %v", rep2.Changed)
	}
}

func TestOpenCodeInstallRefusesInvalidConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := filepath.Join(home, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	inst := &OpenCodeInstaller{DataDir: filepath.Join(home, ".tma1"), Port: 14318, Logger: slog.Default()}
	if _, _, err := inst.installMCPServer(); err == nil {
		t.Fatal("installMCPServer should refuse invalid JSON")
	}
}
