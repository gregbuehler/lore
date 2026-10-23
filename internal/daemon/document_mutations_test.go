package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func writeLinkTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
}

func readLinkTestFile(t *testing.T, root, rel string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestDocumentMutationRequiresCurrentRevision(t *testing.T) {
	for _, operation := range []string{"delete", "move"} {
		for _, base := range []string{"", "stale"} {
			t.Run(operation+"/"+base, func(t *testing.T) {
				d, root, _ := documentTestDaemon(t)
				writeLinkTestFile(t, root, "target.md", "new content")
				writeLinkTestFile(t, root, "inbound.md", "[[target]]")
				req := &Request{SourceID: "vault", RelPath: "target.md", NewPath: "moved.md", BaseRevision: base}
				var response *Response
				if operation == "delete" {
					response = d.dispatchDocumentDelete(req)
				} else {
					response = d.dispatchDocumentMove(req)
				}
				if response.OK || response.Code != "conflict" {
					t.Fatalf("mutation = %#v", response)
				}
				if got := readLinkTestFile(t, root, "target.md"); got != "new content" {
					t.Fatalf("current content overwritten: %q", got)
				}
				if got := readLinkTestFile(t, root, "inbound.md"); got != "[[target]]" {
					t.Fatalf("inbound changed on conflict: %q", got)
				}
				if _, err := os.Stat(filepath.Join(root, "moved.md")); !os.IsNotExist(err) {
					t.Fatalf("destination created on conflict: %v", err)
				}
			})
		}
	}
}

func TestDocumentMoveDoesNotOverwriteDestination(t *testing.T) {
	d, root, _ := documentTestDaemon(t)
	writeLinkTestFile(t, root, "old.md", "original")
	writeLinkTestFile(t, root, "new.md", "destination")
	response := d.dispatchDocumentMove(&Request{SourceID: "vault", RelPath: "old.md", NewPath: "new.md", BaseRevision: revision([]byte("original"))})
	if response.OK || response.Code != "conflict" {
		t.Fatalf("move = %#v", response)
	}
	if readLinkTestFile(t, root, "old.md") != "original" || readLinkTestFile(t, root, "new.md") != "destination" {
		t.Fatal("collision changed a document")
	}
}

func TestDocumentMoveRewritesSourceLocalLinksAndPreservesIdentity(t *testing.T) {
	d, root, library := documentTestDaemon(t)
	original := "---\ncustom: retain\naliases: [Old Alias]\n---\n# Original\n[peer](peer.md#details)\n[[peer|Peer]]\n![asset](image.png)\n[self](old.md#self)\n"
	inbound := "[[folder/old#Heading|Label]]\n[md](folder/old.md#Heading \"Title\")\n[alias][old]\n[old]: folder/old.md\n`[[folder/old]]`\n`` [code](folder/old.md) ``\n````md\n[[folder/old]]\n```\n[code](folder/old.md)\n````\n~~~\n[[folder/old]]\n~~~\n"
	writeLinkTestFile(t, root, "folder/old.md", original)
	writeLinkTestFile(t, root, "folder/peer.md", "# Peer")
	writeLinkTestFile(t, root, "inbound.md", inbound)
	writeLinkTestFile(t, root, "alias.md", "[[Old Alias]]")
	writeLinkTestFile(t, library, "inbound.md", "[[folder/old]] [md](folder/old.md)")
	before := d.dispatchDocumentRead(&Request{SourceID: "vault", RelPath: "folder/old.md"})
	if !before.OK || before.Document == nil || before.Document.DocumentID == "" {
		t.Fatalf("read identity = %#v", before)
	}
	response := d.dispatchDocumentMove(&Request{SourceID: "vault", RelPath: "folder/old.md", NewPath: "other/New Note.md", BaseRevision: revision([]byte(original))})
	if !response.OK || response.Document == nil {
		t.Fatalf("move = %#v", response)
	}
	if response.Document.DocumentID != before.Document.DocumentID || response.Document.CreatedAt != before.Document.CreatedAt {
		t.Fatalf("identity changed: before=%#v after=%#v", before.Document, response.Document)
	}
	if response.Document.Metadata["custom"] != "retain" {
		t.Fatalf("metadata changed: %#v", response.Document.Metadata)
	}
	wantMoved := "---\ncustom: retain\naliases: [Old Alias]\n---\n# Original\n[peer](../folder/peer.md#details)\n[[folder/peer|Peer]]\n![asset](../folder/image.png)\n[self](New%20Note.md#self)\n"
	if got := readLinkTestFile(t, root, "other/New Note.md"); got != wantMoved {
		t.Fatalf("moved content:\n%s\nwant:\n%s", got, wantMoved)
	}
	wantInbound := strings.Replace(inbound, "[[folder/old#Heading|Label]]", "[[other/New%20Note#Heading|Label]]", 1)
	wantInbound = strings.Replace(wantInbound, "[md](folder/old.md#Heading", "[md](other/New%20Note.md#Heading", 1)
	wantInbound = strings.Replace(wantInbound, "[old]: folder/old.md", "[old]: other/New%20Note.md", 1)
	if got := readLinkTestFile(t, root, "inbound.md"); got != wantInbound {
		t.Fatalf("inbound content:\n%s\nwant:\n%s", got, wantInbound)
	}
	if got := readLinkTestFile(t, root, "alias.md"); got != "[[other/New%20Note]]" {
		t.Fatalf("frontmatter alias rewrite = %q", got)
	}
	if got := readLinkTestFile(t, library, "inbound.md"); got != "[[folder/old]] [md](folder/old.md)" {
		t.Fatalf("another source was rewritten: %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "folder/old.md")); !os.IsNotExist(err) {
		t.Fatalf("old document remains: %v", err)
	}
	info, err := os.Stat(filepath.Join(root, "other/New Note.md"))
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("mode not preserved: %v %v", info, err)
	}
}

func TestDocumentDeleteIsSourceLocalAndRemovesIdentity(t *testing.T) {
	d, root, library := documentTestDaemon(t)
	writeLinkTestFile(t, root, "target.md", "# Target")
	writeLinkTestFile(t, library, "target.md", "# Other Library")
	before := d.dispatchDocumentRead(&Request{SourceID: "vault", RelPath: "target.md"})
	if !before.OK || before.Document == nil {
		t.Fatalf("read = %#v", before)
	}
	response := d.dispatchDocumentDelete(&Request{SourceID: "vault", RelPath: "target.md", BaseRevision: before.Document.Revision})
	if !response.OK {
		t.Fatalf("delete = %#v", response)
	}
	if _, err := os.Stat(filepath.Join(root, "target.md")); !os.IsNotExist(err) {
		t.Fatalf("deleted document remains: %v", err)
	}
	if got := readLinkTestFile(t, library, "target.md"); got != "# Other Library" {
		t.Fatalf("another source changed: %q", got)
	}
	created := d.dispatchDocumentWrite(&Request{SourceID: "vault", RelPath: "target.md", Content: "# Recreated"})
	if !created.OK || created.Document == nil || created.Document.DocumentID == before.Document.DocumentID {
		t.Fatalf("recreation reused deleted identity: %#v", created)
	}
}

func TestDocumentMutationsRejectReadonlyAndUnsafePaths(t *testing.T) {
	d, root, library := documentTestDaemon(t)
	writeLinkTestFile(t, library, "target.md", "original")
	writeLinkTestFile(t, root, "target.md", "original")
	for _, method := range []func(*Request) *Response{d.dispatchDocumentMove, d.dispatchDocumentDelete} {
		response := method(&Request{SourceID: "library:notes", RelPath: "target.md", NewPath: "moved.md", BaseRevision: revision([]byte("original"))})
		if response.OK || response.Code != "readonly" {
			t.Fatalf("readonly mutation = %#v", response)
		}
	}
	for _, destination := range []string{"../escape.md", ".private/file.md", "folder/../target.md"} {
		response := d.dispatchDocumentMove(&Request{SourceID: "vault", RelPath: "target.md", NewPath: destination, BaseRevision: revision([]byte("original"))})
		if response.OK || response.Code != "invalid_path" {
			t.Fatalf("unsafe move %q = %#v", destination, response)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	response := d.dispatchDocumentMove(&Request{SourceID: "vault", RelPath: "target.md", NewPath: "escape/moved.md", BaseRevision: revision([]byte("original"))})
	if response.OK || response.Code != "invalid_path" {
		t.Fatalf("symlink move = %#v", response)
	}
}

func TestDocumentMoveSerializesWithConcurrentWrite(t *testing.T) {
	d, root, _ := documentTestDaemon(t)
	writeLinkTestFile(t, root, "target.md", "original")
	base := revision([]byte("original"))
	var wait sync.WaitGroup
	wait.Add(2)
	var moved, written *Response
	go func() {
		defer wait.Done()
		moved = d.dispatchDocumentMove(&Request{SourceID: "vault", RelPath: "target.md", NewPath: "moved.md", BaseRevision: base})
	}()
	go func() {
		defer wait.Done()
		written = d.dispatchDocumentWrite(&Request{SourceID: "vault", RelPath: "target.md", Content: "edited", BaseRevision: base})
	}()
	wait.Wait()
	if moved.OK == written.OK {
		t.Fatalf("expected exactly one winner: move=%#v write=%#v", moved, written)
	}
	if written.OK {
		if got := readLinkTestFile(t, root, "target.md"); got != "edited" || moved.Code != "conflict" {
			t.Fatalf("concurrent edit lost: %q, %#v", got, moved)
		}
	} else if got := readLinkTestFile(t, root, "moved.md"); got != "original" || written.Code != "conflict" {
		t.Fatalf("move lost: %q, %#v", got, written)
	}
}

func TestMovePreapplyRevisionCheckDetectsFilesystemEdits(t *testing.T) {
	d, root, _ := documentTestDaemon(t)
	writeLinkTestFile(t, root, "target.md", "original")
	source, err := d.source("vault")
	if err != nil {
		t.Fatal(err)
	}
	index, err := scanDocumentLinks(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range index.docs {
		if file.rel != "target.md" {
			continue
		}
		if !unchangedDocument(source, file) {
			t.Fatal("unchanged snapshot rejected")
		}
		writeLinkTestFile(t, root, "target.md", "external edit")
		if unchangedDocument(source, file) {
			t.Fatal("stale filesystem snapshot accepted for apply")
		}
		if got := readLinkTestFile(t, root, "target.md"); got != "external edit" {
			t.Fatalf("external edit overwritten: %q", got)
		}
		return
	}
	t.Fatal("source document missing from snapshot")
}

func TestDocumentMovePreservesOrderedListFencesAndBacklinks(t *testing.T) {
	for _, marker := range []string{"1.", "1)", "123456789."} {
		t.Run(marker, func(t *testing.T) {
			d, root, _ := documentTestDaemon(t)
			indent := strings.Repeat(" ", len(marker)+1)
			code := marker + " ~~~\n" + indent + "[[old]]\n" + indent + "~~~\n"
			writeLinkTestFile(t, root, "old.md", "# Original")
			writeLinkTestFile(t, root, "code.md", code)
			writeLinkTestFile(t, root, "inbound.md", code+"\n[[old]]\n")
			before := d.dispatchBacklinks(&Request{SourceID: "vault", RelPath: "old.md"})
			if !before.OK || len(before.Documents) != 1 || before.Documents[0].RelPath != "inbound.md" {
				t.Fatalf("backlinks before move = %#v", before)
			}
			moved := d.dispatchDocumentMove(&Request{SourceID: "vault", RelPath: "old.md", NewPath: "new.md", BaseRevision: revision([]byte("# Original"))})
			if !moved.OK {
				t.Fatalf("move = %#v", moved)
			}
			if got := readLinkTestFile(t, root, "code.md"); got != code {
				t.Fatalf("code-only document changed: %q", got)
			}
			if got := readLinkTestFile(t, root, "inbound.md"); got != code+"\n[[new]]\n" {
				t.Fatalf("rewritten Markdown = %q", got)
			}
			after := d.dispatchBacklinks(&Request{SourceID: "vault", RelPath: "new.md"})
			if !after.OK || len(after.Documents) != 1 || after.Documents[0].DocumentID != before.Documents[0].DocumentID {
				t.Fatalf("backlinks after move = %#v", after)
			}
		})
	}
}

func TestDocumentMoveEncodesWikiFilenamesWithoutChangingSuffixes(t *testing.T) {
	for _, test := range []struct{ filename, wikiPath, markdownPath string }{
		{"new#draft.md", "new%23draft", "new%23draft.md"},
		{"new|draft.md", "new%7Cdraft", "new%7Cdraft.md"},
		{"new?draft.md", "new%3Fdraft", "new%3Fdraft.md"},
		{"new%23draft.md", "new%2523draft", "new%2523draft.md"},
		{"new note.md", "new%20note", "new%20note.md"},
		{"new雪.md", "new%E9%9B%AA", "new%E9%9B%AA.md"},
		{"new.md.md", "new.md.md", "new.md.md"},
		{" new .md", "%20new%20", "%20new%20.md"},
		{"Meeting: notes.md", "Meeting%3A%20notes", "Meeting%3A%20notes.md"},
	} {
		t.Run(test.filename, func(t *testing.T) {
			d, root, _ := documentTestDaemon(t)
			writeLinkTestFile(t, root, "reserved.md", "# Original")
			writeLinkTestFile(t, root, "new.md", "# Unrelated")
			if test.filename != "new#draft.md" {
				writeLinkTestFile(t, root, "new#draft.md", "# Decoding trap")
			}
			inbound := "[[reserved#Section|Alias]]\n[[reserved.md|Explicit]]\n[Markdown](reserved.md#Section)\n"
			writeLinkTestFile(t, root, "inbound.md", inbound)
			before := d.dispatchDocumentRead(&Request{SourceID: "vault", RelPath: "reserved.md"})
			if !before.OK || before.Document == nil {
				t.Fatalf("read = %#v", before)
			}
			moved := d.dispatchDocumentMove(&Request{SourceID: "vault", RelPath: "reserved.md", NewPath: test.filename, BaseRevision: before.Document.Revision})
			if !moved.OK || moved.Document == nil || moved.Document.DocumentID != before.Document.DocumentID {
				t.Fatalf("move = %#v", moved)
			}
			want := "[[" + test.wikiPath + "#Section|Alias]]\n[[" + test.markdownPath + "|Explicit]]\n[Markdown](" + test.markdownPath + "#Section)\n"
			if got := readLinkTestFile(t, root, "inbound.md"); got != want {
				t.Fatalf("rewritten links = %q, want %q", got, want)
			}
			for _, target := range []string{"[[" + test.wikiPath + "#Section|Alias]]", "[[" + test.markdownPath + "|Explicit]]", test.markdownPath + "#Section"} {
				resolved := d.dispatchDocumentResolve(&Request{SourceID: "vault", FromPath: "inbound.md", Target: target})
				if !resolved.OK || resolved.Resolution == nil || !resolved.Resolution.Resolved || resolved.Resolution.DocumentID != before.Document.DocumentID {
					t.Fatalf("resolve %q = %#v", target, resolved)
				}
			}
			backlinks := d.dispatchBacklinks(&Request{SourceID: "vault", RelPath: test.filename})
			if !backlinks.OK || len(backlinks.Documents) != 1 || backlinks.Documents[0].RelPath != "inbound.md" {
				t.Fatalf("backlinks = %#v", backlinks)
			}
			returned := d.dispatchDocumentMove(&Request{SourceID: "vault", RelPath: test.filename, NewPath: "reserved.md", BaseRevision: moved.Document.Revision})
			if !returned.OK || returned.Document == nil || returned.Document.DocumentID != before.Document.DocumentID {
				t.Fatalf("return move = %#v", returned)
			}
			wantReturn := inbound
			if test.filename == "new.md.md" {
				wantReturn = strings.Replace(wantReturn, "[[reserved#Section", "[[reserved.md#Section", 1)
			}
			if got := readLinkTestFile(t, root, "inbound.md"); got != wantReturn {
				t.Fatalf("returned links = %q, want %q", got, wantReturn)
			}
		})
	}
}
