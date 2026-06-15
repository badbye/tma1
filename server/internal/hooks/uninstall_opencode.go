package hooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// OpenCodeUninstaller removes the plugin and MCP entry installed by
// OpenCodeInstaller, leaving user-authored OpenCode config intact.
type OpenCodeUninstaller struct {
	DataDir    string
	ProjectDir string
	Logger     *slog.Logger
	DryRun     bool
	PurgeData  bool
}

func (u *OpenCodeUninstaller) writeFile(path string, data []byte, perm os.FileMode) error {
	if u.DryRun {
		if u.Logger != nil {
			u.Logger.Info("[dry-run] would write", "path", path, "bytes", len(data))
		}
		return nil
	}
	return writeFileAtomic(path, data, perm)
}

func (u *OpenCodeUninstaller) mkdirAll(path string, perm os.FileMode) error {
	if u.DryRun {
		if u.Logger != nil {
			u.Logger.Info("[dry-run] would mkdir -p", "path", path)
		}
		return nil
	}
	return os.MkdirAll(path, perm)
}

func (u *OpenCodeUninstaller) dryRun() bool            { return u.DryRun }
func (u *OpenCodeUninstaller) getLogger() *slog.Logger { return u.Logger }

func (u *OpenCodeUninstaller) Uninstall() (UninstallReport, error) {
	var rep UninstallReport
	var errs []error
	home, err := os.UserHomeDir()
	if err != nil {
		return rep, fmt.Errorf("resolve home: %w", err)
	}

	pluginPath := filepath.Join(home, ".config", "opencode", "plugins", "tma1.js")
	rep.SettingsPath = pluginPath
	if removed, err := u.removeFile(pluginPath); err != nil {
		errs = append(errs, fmt.Errorf("plugin: %w", err))
	} else if removed {
		rep.Removed = append(rep.Removed, reportPath(pluginPath, "OpenCode plugin"))
	} else {
		rep.Skipped = append(rep.Skipped, reportPath(pluginPath, "not present"))
	}

	if path, removed, err := u.uninstallMCP(home); err != nil {
		errs = append(errs, fmt.Errorf("opencode.json: %w", err))
		rep.MCPConfigPath = path
	} else {
		rep.MCPConfigPath = path
		if removed {
			rep.Removed = append(rep.Removed, reportPath(path, "mcp.tma1"))
		} else {
			rep.Skipped = append(rep.Skipped, reportPath(path, "no tma1 MCP entry"))
		}
	}

	if u.ProjectDir != "" {
		target := chooseInstructionsFile(u.ProjectDir, "AGENTS.md")
		removed, err := u.uninstallInstructionsFile(target)
		switch {
		case errors.Is(err, ErrInstructionsHalfState):
			rep.Errors = append(rep.Errors, reportPath(target, err.Error()))
		case err != nil:
			errs = append(errs, fmt.Errorf("%s: %w", filepath.Base(target), err))
		case removed:
			rep.InstructionsPaths = append(rep.InstructionsPaths, target)
			rep.Removed = append(rep.Removed, reportPath(target, "tma1 block"))
		}

		gi := filepath.Join(u.ProjectDir, ".gitignore")
		if _, err := os.Stat(gi); err == nil {
			rep.GitignorePath = gi
			rep.Skipped = append(rep.Skipped, reportPath(gi, "left in place — uninstall does not delete; manually remove '.tma1-context.md' if desired"))
		}
	}

	if u.PurgeData {
		for _, sub := range []string{"data", "bin"} {
			p := filepath.Join(u.DataDir, sub)
			if u.DryRun {
				if u.Logger != nil {
					u.Logger.Info("[dry-run] would purge", "path", p)
				}
				rep.Removed = append(rep.Removed, reportPath(p, "purge-data"))
				continue
			}
			if _, err := os.Stat(p); err == nil {
				if err := os.RemoveAll(p); err != nil {
					errs = append(errs, fmt.Errorf("purge %s: %w", p, err))
				} else {
					rep.Removed = append(rep.Removed, reportPath(p, "purge-data"))
				}
			}
		}
	}

	if len(errs) > 0 {
		return rep, joinErrors(errs)
	}
	return rep, nil
}

func (u *OpenCodeUninstaller) removeFile(path string) (bool, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, nil
	}
	if u.DryRun {
		if u.Logger != nil {
			u.Logger.Info("[dry-run] would remove", "path", path)
		}
		return true, nil
	}
	if err := os.Remove(path); err != nil {
		return false, err
	}
	return true, nil
}

func (u *OpenCodeUninstaller) uninstallMCP(home string) (string, bool, error) {
	path := filepath.Join(home, ".config", "opencode", "opencode.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path, false, nil
	}
	existing, err := readJSONFileStrict(path)
	if err != nil {
		return path, false, fmt.Errorf("refusing to overwrite %s: %w", path, err)
	}
	mcp, _ := existing["mcp"].(map[string]any)
	if !removeMCPServerEntry(mcp, hookOwnerID) {
		return path, false, nil
	}
	existing["mcp"] = mcp
	out, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return path, false, err
	}
	if err := u.writeFile(path, append(out, '\n'), 0o644); err != nil {
		return path, false, err
	}
	return path, true, nil
}

func (u *OpenCodeUninstaller) uninstallInstructionsFile(path string) (bool, error) {
	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	newContent, removed, err := removeInstructionsBlock(existing)
	if err != nil {
		return false, err
	}
	if !removed {
		return false, nil
	}
	if err := u.writeFile(path, newContent, 0o644); err != nil {
		return false, err
	}
	return true, nil
}
