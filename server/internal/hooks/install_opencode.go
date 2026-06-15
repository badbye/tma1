package hooks

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

//go:embed opencode-plugin.js.tmpl
var opencodePluginTemplate string

// OpenCodeInstaller wires OpenCode into TMA1 via a native plugin plus MCP.
// OpenCode has no Claude/Codex-style hook script; the plugin normalizes its
// events into /api/hooks and /api/messages.
type OpenCodeInstaller struct {
	DataDir            string
	Port               int
	GreptimeDBHTTPPort int
	ProjectDir         string
	Logger             *slog.Logger
	DryRun             bool
}

func (i *OpenCodeInstaller) writeFile(path string, data []byte, perm os.FileMode) error {
	if i.DryRun {
		if i.Logger != nil {
			i.Logger.Info("[dry-run] would write", "path", path, "bytes", len(data))
		}
		return nil
	}
	return writeFileAtomic(path, data, perm)
}

func (i *OpenCodeInstaller) mkdirAll(path string, perm os.FileMode) error {
	if i.DryRun {
		if i.Logger != nil {
			i.Logger.Info("[dry-run] would mkdir -p", "path", path)
		}
		return nil
	}
	return os.MkdirAll(path, perm)
}

func (i *OpenCodeInstaller) dryRun() bool            { return i.DryRun }
func (i *OpenCodeInstaller) getLogger() *slog.Logger { return i.Logger }

func (i *OpenCodeInstaller) Install() (InstallReport, error) {
	var rep InstallReport
	var errs []error

	pluginPath, pluginChanged, err := i.installPlugin()
	if err != nil {
		errs = append(errs, fmt.Errorf("plugin: %w", err))
	}
	rep.SettingsPath = pluginPath
	if pluginChanged {
		rep.Changed = append(rep.Changed, pluginPath+" (OpenCode plugin)")
	}

	if i.ProjectDir != "" {
		instrPath, changed, err := installInstructions(i, i.ProjectDir, "AGENTS.md")
		if err != nil {
			errs = append(errs, fmt.Errorf("instructions: %w", err))
		}
		rep.InstructionsPath = instrPath
		if changed {
			rep.Changed = append(rep.Changed, instrPath+" (tma1 block)")
		}

		gi, changed, err := installGitignore(i, i.ProjectDir)
		if err != nil {
			errs = append(errs, fmt.Errorf("gitignore: %w", err))
		}
		rep.GitignorePath = gi
		if changed {
			rep.Changed = append(rep.Changed, gi+" (.tma1-context.md entry)")
		}
	}

	mcpPath, mcpChanged, err := i.installMCPServer()
	if err != nil {
		errs = append(errs, fmt.Errorf("mcp config: %w", err))
	}
	rep.MCPConfigPath = mcpPath
	if mcpChanged {
		rep.Changed = append(rep.Changed, mcpPath+" (mcp.tma1)")
	}

	if len(errs) > 0 {
		return rep, joinErrors(errs)
	}
	return rep, nil
}

func (i *OpenCodeInstaller) installPlugin() (string, bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, err
	}
	path := filepath.Join(home, ".config", "opencode", "plugins", "tma1.js")
	if err := i.mkdirAll(filepath.Dir(path), 0o755); err != nil {
		return path, false, err
	}
	content := strings.ReplaceAll(opencodePluginTemplate, "{{PORT}}", strconv.Itoa(i.Port))
	existing, err := os.ReadFile(path)
	if err == nil && string(existing) == content {
		return path, false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return path, false, err
	}
	if err := i.writeFile(path, []byte(content), 0o644); err != nil {
		return path, false, err
	}
	return path, true, nil
}

func (i *OpenCodeInstaller) installMCPServer() (string, bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, err
	}
	cfgPath := filepath.Join(home, ".config", "opencode", "opencode.json")
	if err := i.mkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return cfgPath, false, err
	}
	existing, err := readJSONFileStrict(cfgPath)
	if err != nil {
		return cfgPath, false, fmt.Errorf("refusing to overwrite %s: %w", cfgPath, err)
	}
	binary, err := tma1BinaryPath(i.DataDir)
	if err != nil {
		return cfgPath, false, err
	}
	mcp, _ := existing["mcp"].(map[string]any)
	if mcp == nil {
		mcp = map[string]any{}
	}
	desired := map[string]any{
		"type":    "local",
		"command": []any{binary, "mcp-serve"},
		"enabled": true,
		"environment": map[string]any{
			"TMA1_MCP_CALLER": "opencode",
		},
	}
	if i.GreptimeDBHTTPPort != 0 && i.GreptimeDBHTTPPort != defaultGreptimeDBHTTPPort {
		desired["environment"].(map[string]any)["TMA1_GREPTIMEDB_HTTP_PORT"] = strconv.Itoa(i.GreptimeDBHTTPPort)
	}
	if cur, ok := mcp[hookOwnerID].(map[string]any); ok && opencodeMCPEntryEqual(cur, desired) {
		return cfgPath, false, nil
	}
	mcp[hookOwnerID] = desired
	existing["mcp"] = mcp
	out, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return cfgPath, false, err
	}
	if err := i.writeFile(cfgPath, append(out, '\n'), 0o644); err != nil {
		return cfgPath, false, err
	}
	return cfgPath, true, nil
}

func opencodeMCPEntryEqual(a, b map[string]any) bool {
	at, _ := a["type"].(string)
	bt, _ := b["type"].(string)
	if at != bt {
		return false
	}
	aj, _ := json.Marshal(a["command"])
	bj, _ := json.Marshal(b["command"])
	if string(aj) != string(bj) {
		return false
	}
	ae, _ := json.Marshal(a["environment"])
	be, _ := json.Marshal(b["environment"])
	if string(ae) != string(be) {
		return false
	}
	ab, _ := a["enabled"].(bool)
	bb, _ := b["enabled"].(bool)
	return ab == bb
}
