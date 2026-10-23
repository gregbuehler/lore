package daemon

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrepareSocketPathRejectsNonSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.sock")
	if err := os.WriteFile(path, []byte("not a socket"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	err := prepareSocketPath(path)
	if err == nil {
		t.Fatal("expected non-socket rejection")
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("non-socket was removed: %v", statErr)
	}
}

func TestStartWithExternalSocketAndFreshHome(t *testing.T) {
	if os.Getenv("LORE_START_TEST_CHILD") == "1" {
		if err := Start(os.Getenv("LORE_START_TEST_VAULT"), nil); err != nil {
			t.Fatal(err)
		}
		return
	}
	root, err := os.MkdirTemp("/tmp", "lore-start-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	vault := filepath.Join(root, "vault")
	if err := os.MkdirAll(filepath.Join(vault, ".lore"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, ".lore", "config.yaml"), []byte("vault:\n  path: "+vault+"\nsubscriptions: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(root, "daemon.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStartWithExternalSocketAndFreshHome$")
	for _, entry := range os.Environ() {
		key := strings.SplitN(entry, "=", 2)[0]
		switch key {
		case "HOME", "LORE_SOCKET", "LORE_DB", "LORE_API_TOKEN", "LORE_START_TEST_CHILD", "LORE_START_TEST_VAULT":
			continue
		}
		command.Env = append(command.Env, entry)
	}
	command.Env = append(command.Env, "HOME="+filepath.Join(root, "fresh-home"), "LORE_SOCKET="+socket, "LORE_DB="+filepath.Join(root, "index"), "LORE_API_TOKEN=", "LORE_START_TEST_CHILD=1", "LORE_START_TEST_VAULT="+vault)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = command.Stdout
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		_ = command.Wait()
	}()
	ready := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		var output strings.Builder
		signalled := false
		for scanner.Scan() {
			line := scanner.Text()
			output.WriteString(line + "\n")
			if !signalled && strings.Contains(line, "lore daemon: listening on ") {
				signalled = true
				ready <- nil
			}
		}
		if !signalled {
			ready <- fmt.Errorf("daemon exited before serving: %s", output.String())
		}
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not begin serving")
	}
	connection, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	client := &Client{conn: connection}
	response, err := client.Send(&Request{Type: "sources"})
	if err != nil || !response.OK || len(response.Sources) != 1 || response.Sources[0].SourceID != "vault" {
		t.Fatalf("fresh daemon did not serve its canonical vault: response=%+v err=%v", response, err)
	}
}
