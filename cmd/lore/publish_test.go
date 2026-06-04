package lore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatPublishStatusesIncludesEachTargetPorcelain(t *testing.T) {
	output := formatPublishStatuses([]publishChange{
		{
			name:            "services",
			path:            "/tmp/services",
			stageableStatus: " M README.md\n",
			skippedStatus:   "?? runbook.md\n",
		},
		{
			name:            "infra",
			path:            "/tmp/infra",
			stageableStatus: "A  deploy.yaml\n",
		},
	}, false)

	for _, want := range []string{
		"Pending changes to publish:",
		"services (/tmp/services)",
		"staged:",
		"      M README.md",
		"not staged by lore:",
		"      ?? runbook.md",
		"infra (/tmp/infra)",
		"      A  deploy.yaml",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("formatPublishStatuses() missing %q in:\n%s", want, output)
		}
	}
}

func TestFormatPublishStatusesDescribesManagedPathStaging(t *testing.T) {
	output := formatPublishStatuses([]publishChange{{
		name:            "services",
		path:            "/tmp/services",
		stageableStatus: " M README.md\n",
	}}, false)

	if !strings.Contains(output, "Staging mode: lore-managed library content paths only") {
		t.Fatalf("formatPublishStatuses() missing managed-path staging mode in:\n%s", output)
	}
	if strings.Contains(output, "git add -A") {
		t.Fatalf("formatPublishStatuses() unexpectedly mentions all-repo staging in:\n%s", output)
	}
}

func TestFormatPublishStatusesDescribesAllRepoStaging(t *testing.T) {
	output := formatPublishStatuses([]publishChange{{
		name:            "services",
		path:            "/tmp/services",
		stageableStatus: " M README.md\n",
	}}, true)

	if !strings.Contains(output, "Staging mode: all repository changes (--all / git add -A)") {
		t.Fatalf("formatPublishStatuses() missing --all staging mode in:\n%s", output)
	}
}

func TestPublishStageArgsUsesAllRepoStagingWhenRequested(t *testing.T) {
	args := publishStageArgs(true, []string{"Wiki", "library.yaml"})

	want := []string{"add", "-A"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("publishStageArgs(true) = %#v, want %#v", args, want)
	}
}

func TestPublishStageArgsUsesManagedPathsByDefault(t *testing.T) {
	args := publishStageArgs(false, []string{"Wiki", "library.yaml"})

	want := []string{"add", "-A", "--", "Wiki", "library.yaml"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("publishStageArgs(false) = %#v, want %#v", args, want)
	}
}

func TestPublishStageArgsRejectsEmptyManagedPaths(t *testing.T) {
	args := publishStageArgs(false, nil)

	if args != nil {
		t.Fatalf("publishStageArgs(false, nil) = %#v, want nil", args)
	}
}

func TestPublishManagedPathsIncludesExistingLorePaths(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "Wiki"), 0o755); err != nil {
		t.Fatalf("mkdir Wiki: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "library.yaml"), []byte("name: test\n"), 0o644); err != nil {
		t.Fatalf("write library.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Readme\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Agents\n"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".DS_Store\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	paths, err := publishManagedPaths(dir)
	if err != nil {
		t.Fatalf("publishManagedPaths() returned error: %v", err)
	}

	got := strings.Join(paths, "\x00")
	for _, want := range []string{"Wiki", "library.yaml", "README.md", "AGENTS.md", ".gitignore"} {
		if !strings.Contains(got, want) {
			t.Fatalf("publishManagedPaths() = %#v, want %q", paths, want)
		}
	}
}

func TestPublishManagedPathsPrefixesSubscriptionRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs", "Wiki"), 0o755); err != nil {
		t.Fatalf("mkdir docs/Wiki: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "library.yaml"), []byte("name: test\n"), 0o644); err != nil {
		t.Fatalf("write docs/library.yaml: %v", err)
	}

	paths, err := publishManagedPathsForRoot(dir, "docs")
	if err != nil {
		t.Fatalf("publishManagedPathsForRoot() returned error: %v", err)
	}

	got := strings.Join(paths, "\x00")
	for _, want := range []string{"docs/Wiki", "docs/library.yaml"} {
		if !strings.Contains(got, want) {
			t.Fatalf("publishManagedPathsForRoot() = %#v, want %q", paths, want)
		}
	}
}

func TestPublishManagedPathsRejectsAbsoluteRoot(t *testing.T) {
	if _, err := publishManagedPathsForRoot(t.TempDir(), "/docs"); err == nil {
		t.Fatal("publishManagedPathsForRoot() error = nil, want absolute root rejection")
	}
}

func TestSplitPublishStatusSeparatesManagedAndUnmanagedPaths(t *testing.T) {
	stageable, skipped := splitPublishStatus(" M Wiki/page.md\n?? scratch.txt\nR  old.md -> README.md\n", []string{"Wiki", "README.md"})

	for _, want := range []string{" M Wiki/page.md", "R  old.md -> README.md"} {
		if !strings.Contains(stageable, want) {
			t.Fatalf("stageable status = %q, want %q", stageable, want)
		}
	}
	if !strings.Contains(skipped, "?? scratch.txt") {
		t.Fatalf("skipped status = %q, want unmanaged path", skipped)
	}
}

func TestConfirmPublishAcceptsYesResponses(t *testing.T) {
	for _, input := range []string{"y\n", "yes\n", "Y\n", "YES\n"} {
		var out bytes.Buffer
		ok, err := confirmPublish(strings.NewReader(input), &out)
		if err != nil {
			t.Fatalf("confirmPublish(%q) returned error: %v", input, err)
		}
		if !ok {
			t.Fatalf("confirmPublish(%q) = false, want true", input)
		}
		if !strings.Contains(out.String(), "Publish these changes?") {
			t.Fatalf("prompt output = %q, want confirmation prompt", out.String())
		}
	}
}

func TestConfirmPublishRejectsNonYesResponses(t *testing.T) {
	for _, input := range []string{"n\n", "\n", "no\n"} {
		var out bytes.Buffer
		ok, err := confirmPublish(strings.NewReader(input), &out)
		if err != nil {
			t.Fatalf("confirmPublish(%q) returned error: %v", input, err)
		}
		if ok {
			t.Fatalf("confirmPublish(%q) = true, want false", input)
		}
	}
}
