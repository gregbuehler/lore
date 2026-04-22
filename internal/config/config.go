package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	LoreDir          = ".lore"
	ConfigFile       = "config.yaml"
	LibrariesBaseDir = ".lore-libraries"
)

// Config is the main lore configuration stored in <vault>/.lore/config.yaml
type Config struct {
	Vault         VaultConfig          `yaml:"vault"`
	DefaultHost   string               `yaml:"default_host,omitempty"` // e.g. "github.example.com", "gitlab.internal.io"
	Registry      []string             `yaml:"registry,omitempty"`
	Subscriptions []SubscriptionConfig `yaml:"subscriptions,omitempty"`
	Agent         AgentConfig          `yaml:"agent,omitempty"`
	MetaIndex     string               `yaml:"meta_index,omitempty"`
	Identity      IdentityConfig       `yaml:"identity,omitempty"`
}

// EffectiveHost returns the git host to use for shorthand repo expansion.
// Resolution order:
//  1. Config.DefaultHost (explicit in .lore/config.yaml)
//  2. GH_HOST environment variable
//  3. First non-github.com host in ~/.config/gh/hosts.yml
//
// This supports GitHub Enterprise, self-hosted GitLab, or any git forge.
func (c *Config) EffectiveHost() string {
	if c.DefaultHost != "" {
		return c.DefaultHost
	}
	return DetectHost()
}

// ResolveRepo expands a shorthand like "team/my-library" to a full
// git SSH URL using the effective host. Full URLs are returned as-is.
//
// Examples (with default_host: "git.example.com"):
//   "team/my-library"  → "git@git.example.com:team/my-library.git"
//   "git@host:..."     → returned as-is
//   "https://..."      → returned as-is
func (c *Config) ResolveRepo(ref string) (string, error) {
	// Already a full URL
	if strings.HasPrefix(ref, "git@") || strings.HasPrefix(ref, "https://") || strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "ssh://") {
		return ref, nil
	}

	// Shorthand: org/repo — expand with effective host
	host := c.EffectiveHost()
	if host == "" {
		return "", fmt.Errorf("%q looks like a shorthand (org/repo) but no default_host is configured and none could be detected from environment", ref)
	}

	repo := strings.TrimSuffix(ref, ".git")
	return fmt.Sprintf("git@%s:%s.git", host, repo), nil
}

// DetectHost attempts to discover a git forge host from the environment.
// It checks GH_HOST first, then parses ~/.config/gh/hosts.yml for the first
// non-github.com host. Returns empty string if nothing is found.
//
// This works for GitHub Enterprise instances. For self-hosted GitLab or other
// forges, set default_host explicitly in .lore/config.yaml.
func DetectHost() string {
	// 1. GH_HOST env (used by gh CLI)
	if h := os.Getenv("GH_HOST"); h != "" {
		return h
	}

	// 2. Parse ~/.config/gh/hosts.yml
	ghConfigDir := os.Getenv("GH_CONFIG_DIR")
	if ghConfigDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		ghConfigDir = filepath.Join(home, ".config", "gh")
	}

	data, err := os.ReadFile(filepath.Join(ghConfigDir, "hosts.yml"))
	if err != nil {
		return ""
	}

	// Parse as map[string]any — top-level keys are hostnames
	var hosts map[string]any
	if err := yaml.Unmarshal(data, &hosts); err != nil {
		return ""
	}

	// Return first non-github.com host
	for host := range hosts {
		if host != "github.com" {
			return host
		}
	}

	return ""
}

type VaultConfig struct {
	Path string `yaml:"path"`
}

type SubscriptionConfig struct {
	Name   string `yaml:"name"`
	Repo   string `yaml:"repo"`
	Path   string `yaml:"path"`
	Access string `yaml:"access"` // read-only, read-write
}

type AgentConfig struct {
	Command string `yaml:"command,omitempty"`
}

type IdentityConfig struct {
	Name  string `yaml:"name,omitempty"`
	Email string `yaml:"email,omitempty"`
}

// LibrariesDir returns the default path for cloned libraries.
func LibrariesDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, LibrariesBaseDir)
}

// ConfigPath returns the path to config.yaml within a vault.
func ConfigPath(vaultPath string) string {
	return filepath.Join(vaultPath, LoreDir, ConfigFile)
}

// Load reads the config from a vault's .lore/config.yaml.
func Load(vaultPath string) (*Config, error) {
	data, err := os.ReadFile(ConfigPath(vaultPath))
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return &cfg, nil
}

// Save writes the config to a vault's .lore/config.yaml.
func (c *Config) Save(vaultPath string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	path := ConfigPath(vaultPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// FindVault locates the vault directory. Checks LORE_VAULT env first, then
// walks up from the current directory looking for .lore/config.yaml.
func FindVault() (string, error) {
	if v := os.Getenv("LORE_VAULT"); v != "" {
		abs, _ := filepath.Abs(v)
		if _, err := os.Stat(filepath.Join(abs, LoreDir, ConfigFile)); err == nil {
			return abs, nil
		}
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, LoreDir, ConfigFile)); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("not inside a lore vault (no .lore/config.yaml found)")
		}
		dir = parent
	}
}
