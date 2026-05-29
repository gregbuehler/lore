package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Client connects to the lore daemon over a unix socket.
type Client struct {
	conn net.Conn
}

// Connect opens a connection to the daemon socket.
func Connect() (*Client, error) {
	conn, err := net.DialTimeout("unix", SocketPath(), 2*time.Second)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn}, nil
}

// Close closes the client connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// Send sends a request and reads the response.
func (c *Client) Send(req *Request) (*Response, error) {
	if err := writeMessage(c.conn, req); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}
	var resp Response
	if err := readMessage(c.conn, &resp); err != nil {
		return nil, fmt.Errorf("recv: %w", err)
	}
	return &resp, nil
}

// EnsureDaemon connects to the daemon, starting it if necessary.
// If vaultPath is empty, it will not attempt auto-start.
func EnsureDaemon(vaultPath string) (*Client, error) {
	vaultPath = normalizeVaultPath(vaultPath)
	c, err := Connect()
	if err == nil {
		if vaultPath == "" {
			return c, nil
		}
		matches, err := clientServesVault(c, vaultPath)
		if err == nil && matches {
			return c, nil
		}
		c.Close()
		if err != nil {
			return nil, fmt.Errorf("checking daemon vault: %w", err)
		}
		return nil, fmt.Errorf("daemon is serving a different vault; stop it before starting %s", vaultPath)
	}

	if vaultPath == "" {
		return nil, fmt.Errorf("daemon not running and no vault path for auto-start")
	}

	// Auto-start the daemon
	exe, _ := os.Executable()
	cmd := exec.Command(exe, "daemon", "start", "--vault", vaultPath)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("auto-start daemon: %w", err)
	}
	// Detach
	cmd.Process.Release()

	// Poll for socket
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		if c, err := Connect(); err == nil {
			return c, nil
		}
	}
	return nil, fmt.Errorf("daemon did not start within 3s")
}

func normalizeVaultPath(vaultPath string) string {
	if vaultPath == "" {
		return ""
	}
	abs, err := filepath.Abs(vaultPath)
	if err != nil {
		return filepath.Clean(vaultPath)
	}
	return abs
}

func clientServesVault(c *Client, vaultPath string) (bool, error) {
	resp, err := c.Send(&Request{Type: "status"})
	if err != nil {
		return false, err
	}
	if !resp.OK {
		return false, errors.New(resp.Error)
	}
	if resp.Stats == nil {
		return false, fmt.Errorf("daemon status did not include vault path")
	}
	return sameVaultPath(resp.Stats.VaultPath, vaultPath), nil
}

func sameVaultPath(a, b string) bool {
	a = normalizeVaultPath(a)
	b = normalizeVaultPath(b)
	return a != "" && b != "" && a == b
}

// SocketPath returns the unix socket path for the daemon.
func SocketPath() string {
	if p := os.Getenv("LORE_SOCKET"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "lore", "daemon.sock")
}

// PidPath returns the PID file path for the daemon.
func PidPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "lore", "daemon.pid")
}
