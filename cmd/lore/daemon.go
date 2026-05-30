package lore

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"text/template"

	"github.com/gbuehler/lore/internal/config"
	"github.com/gbuehler/lore/internal/daemon"
	"github.com/gbuehler/lore/internal/pathutil"
	"github.com/spf13/cobra"
)

var daemonVault string

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage the lore index daemon",
	Long: `The daemon holds a SQLite index of your vault and subscribed libraries.
It watches for file changes and keeps the index current.

The index is a derived cache — your markdown files are the source of truth.
Without the daemon, 'lore query' still works by querying the SQLite DB
directly (it may be stale until the daemon runs).`,
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the daemon (foreground)",
	Long: `Starts the lore daemon in the foreground. It indexes the vault and
all subscribed libraries, then watches for changes.

Use & to background it, or let 'lore query' auto-start it.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		vaultPath := daemonVault
		if vaultPath == "" {
			var err error
			vaultPath, err = config.FindVault()
			if err != nil {
				return fmt.Errorf("specify --vault or run from within a vault: %w", err)
			}
		}

		// Resolve to absolute path
		abs, err := filepath.Abs(vaultPath)
		if err == nil {
			vaultPath = abs
		}

		cfg, err := config.Load(vaultPath)
		if err != nil {
			return err
		}

		var libPaths []string
		for _, sub := range cfg.Subscriptions {
			libPaths = append(libPaths, sub.Path)
		}

		return daemon.Start(vaultPath, libPaths)
	},
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the running daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		data, err := os.ReadFile(daemon.PidPath())
		if err != nil {
			return fmt.Errorf("daemon not running (no pid file)")
		}
		pid, err := parseDaemonPIDFile(data)
		if err != nil {
			return fmt.Errorf("invalid pid file")
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			return fmt.Errorf("process %d not found", pid)
		}
		if err := proc.Signal(syscall.SIGTERM); err != nil {
			return fmt.Errorf("sending SIGTERM to %d: %w", pid, err)
		}
		fmt.Printf("Sent SIGTERM to daemon (pid %d)\n", pid)
		return nil
	},
}

func parseDaemonPIDFile(data []byte) (int, error) {
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return 0, fmt.Errorf("empty pid file")
	}
	return strconv.Atoi(strings.TrimSpace(lines[0]))
}

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status and index stats",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := daemon.Connect()
		if err != nil {
			fmt.Println("Daemon: not running")
			// Still show DB stats if available
			fmt.Printf("DB: %s\n", daemon.SocketPath())
			return nil
		}
		defer client.Close()

		resp, err := client.Send(&daemon.Request{Type: "status"})
		if err != nil {
			return err
		}
		if !resp.OK {
			return fmt.Errorf("daemon error: %s", resp.Error)
		}

		s := resp.Stats
		fmt.Printf("Daemon: running (socket %s)\n", daemon.SocketPath())
		fmt.Printf("DB:     %s\n", s.DBPath)
		fmt.Printf("Vault:  %s\n", s.VaultPath)
		fmt.Printf("Docs:   %d\n", s.Documents)
		fmt.Printf("Edges:  %d\n", s.Edges)
		fmt.Printf("Paths:  %d\n", s.WatchedDirs)
		return nil
	},
}

var daemonReindexCmd = &cobra.Command{
	Use:   "reindex",
	Short: "Force a full reindex",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := daemon.Connect()
		if err != nil {
			return fmt.Errorf("daemon not running: %w", err)
		}
		defer client.Close()

		resp, err := client.Send(&daemon.Request{Type: "reindex"})
		if err != nil {
			return err
		}
		if !resp.OK {
			return fmt.Errorf("reindex failed: %s", resp.Error)
		}
		if len(resp.Results) > 0 {
			fmt.Println(resp.Results[0].Title)
		}
		return nil
	},
}

const plistLabel = "com.lore.daemon"

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>

    <key>Program</key>
    <string>{{.Program}}</string>

    <key>ProgramArguments</key>
    <array>
        <string>lore</string>
        <string>daemon</string>
        <string>start</string>
        <string>--vault</string>
        <string>{{.VaultPath}}</string>
    </array>

    <key>EnvironmentVariables</key>
    <dict>
        <key>LORE_VAULT</key>
        <string>{{.VaultPath}}</string>
    </dict>

    <key>RunAtLoad</key>
    <true/>

    <key>KeepAlive</key>
    <true/>

    <key>StandardOutPath</key>
    <string>/tmp/lore-daemon.log</string>

    <key>StandardErrorPath</key>
    <string>/tmp/lore-daemon.log</string>
</dict>
</plist>
`

func launchAgentPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding home directory: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist"), nil
}

// resolveVaultAndBinary is shared by install/uninstall to obtain the vault path
// and the absolute path to the running lore binary.
func resolveVaultAndBinary(vaultFlag string) (vaultPath, binPath string, err error) {
	vaultPath = vaultFlag
	if vaultPath == "" {
		if v := os.Getenv("LORE_VAULT"); v != "" {
			vaultPath = v
		} else {
			vaultPath, err = config.FindVault()
			if err != nil {
				return "", "", fmt.Errorf("specify --vault or run from within a vault: %w", err)
			}
		}
	}
	abs, err := filepath.Abs(vaultPath)
	if err == nil {
		vaultPath = abs
	}

	binPath, err = os.Executable()
	if err != nil {
		return "", "", fmt.Errorf("resolving executable path: %w", err)
	}
	return vaultPath, binPath, nil
}

// ── macOS LaunchAgent ────────────────────────────────────────────────────────

func installDarwin(vaultPath, binPath string) error {
	tmpl, err := template.New("plist").Parse(plistTemplate)
	if err != nil {
		return fmt.Errorf("parsing plist template: %w", err)
	}
	data := struct {
		Label     string
		Program   string
		VaultPath string
	}{
		Label:     plistLabel,
		Program:   binPath,
		VaultPath: vaultPath,
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("rendering plist: %w", err)
	}

	plistPath, err := launchAgentPlistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return fmt.Errorf("creating LaunchAgents directory: %w", err)
	}
	if err := pathutil.AtomicWriteFile(plistPath, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing plist: %w", err)
	}
	fmt.Printf("Wrote %s\n", plistPath)

	out, err := exec.Command("launchctl", "load", plistPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl load: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	if msg := strings.TrimSpace(string(out)); msg != "" {
		fmt.Println(msg)
	}
	fmt.Printf("LaunchAgent %s loaded — daemon will start now and at login.\n", plistLabel)
	return nil
}

func uninstallDarwin() error {
	plistPath, err := launchAgentPlistPath()
	if err != nil {
		return err
	}

	// Unload — best-effort; don't abort if it was never loaded.
	out, err := exec.Command("launchctl", "unload", plistPath).CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "launchctl unload: %v\n%s\n", err, strings.TrimSpace(string(out)))
	} else {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			fmt.Println(msg)
		}
		fmt.Printf("LaunchAgent %s unloaded.\n", plistLabel)
	}

	if err := os.Remove(plistPath); err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("Plist not found at %s (already removed).\n", plistPath)
		} else {
			return fmt.Errorf("removing plist: %w", err)
		}
	} else {
		fmt.Printf("Removed %s\n", plistPath)
	}
	return nil
}

// ── Linux systemd user service ───────────────────────────────────────────────

const systemdServiceTemplate = `[Unit]
Description=Lore Index Daemon
After=network.target

[Service]
Type=simple
ExecStart={{.Program}} daemon start --vault {{.VaultPath}}
Restart=on-failure
RestartSec=5
Environment=LORE_VAULT={{.VaultPath}}

[Install]
WantedBy=default.target
`

const systemdServiceName = "lore-daemon.service"

func systemdUnitPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding home directory: %w", err)
	}
	return filepath.Join(home, ".config", "systemd", "user", systemdServiceName), nil
}

func runSystemctl(args ...string) error {
	fullArgs := append([]string{"--user"}, args...)
	out, err := exec.Command("systemctl", fullArgs...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	if msg := strings.TrimSpace(string(out)); msg != "" {
		fmt.Println(msg)
	}
	return nil
}

func installLinux(vaultPath, binPath string) error {
	tmpl, err := template.New("service").Parse(systemdServiceTemplate)
	if err != nil {
		return fmt.Errorf("parsing service template: %w", err)
	}
	data := struct {
		Program   string
		VaultPath string
	}{
		Program:   binPath,
		VaultPath: vaultPath,
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("rendering service unit: %w", err)
	}

	unitPath, err := systemdUnitPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return fmt.Errorf("creating systemd user unit directory: %w", err)
	}
	if err := pathutil.AtomicWriteFile(unitPath, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing service unit: %w", err)
	}
	fmt.Printf("Wrote %s\n", unitPath)

	if err := runSystemctl("daemon-reload"); err != nil {
		return err
	}
	if err := runSystemctl("enable", "--now", systemdServiceName); err != nil {
		return err
	}
	fmt.Printf("systemd user service %s enabled — daemon will start now and at login.\n", systemdServiceName)
	return nil
}

func uninstallLinux() error {
	// Disable and stop — best-effort.
	if err := runSystemctl("disable", "--now", systemdServiceName); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
	} else {
		fmt.Printf("systemd user service %s disabled.\n", systemdServiceName)
	}

	unitPath, err := systemdUnitPath()
	if err != nil {
		return err
	}
	if err := os.Remove(unitPath); err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("Unit file not found at %s (already removed).\n", unitPath)
		} else {
			return fmt.Errorf("removing unit file: %w", err)
		}
	} else {
		fmt.Printf("Removed %s\n", unitPath)
	}

	if err := runSystemctl("daemon-reload"); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
	}
	return nil
}

// ── Cobra commands ───────────────────────────────────────────────────────────

var daemonInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the lore daemon as a system auto-start service",
	Long: `Installs the lore daemon so it starts automatically at login.

On macOS: generates a LaunchAgent plist, installs it to
~/Library/LaunchAgents/, and loads it with launchctl.

On Linux: generates a systemd user service unit, installs it to
~/.config/systemd/user/, and enables it with systemctl --user.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		vaultPath, binPath, err := resolveVaultAndBinary(daemonVault)
		if err != nil {
			return err
		}

		switch runtime.GOOS {
		case "darwin":
			return installDarwin(vaultPath, binPath)
		case "linux":
			return installLinux(vaultPath, binPath)
		default:
			return fmt.Errorf("auto-start installation is not supported on %s", runtime.GOOS)
		}
	},
}

var daemonUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Uninstall the lore daemon auto-start service",
	Long: `Removes the lore daemon from the system auto-start configuration.

On macOS: unloads the LaunchAgent via launchctl and removes the plist from
~/Library/LaunchAgents/.

On Linux: disables the systemd user service via systemctl --user and removes
the unit file from ~/.config/systemd/user/.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		switch runtime.GOOS {
		case "darwin":
			return uninstallDarwin()
		case "linux":
			return uninstallLinux()
		default:
			return fmt.Errorf("auto-start uninstallation is not supported on %s", runtime.GOOS)
		}
	},
}

func init() {
	daemonStartCmd.Flags().StringVar(&daemonVault, "vault", "", "Path to vault (auto-detected if omitted)")
	daemonInstallCmd.Flags().StringVar(&daemonVault, "vault", "", "Path to vault (overrides LORE_VAULT and auto-detection)")
	daemonCmd.AddCommand(daemonStartCmd)
	daemonCmd.AddCommand(daemonStopCmd)
	daemonCmd.AddCommand(daemonStatusCmd)
	daemonCmd.AddCommand(daemonReindexCmd)
	daemonCmd.AddCommand(daemonInstallCmd)
	daemonCmd.AddCommand(daemonUninstallCmd)
}
