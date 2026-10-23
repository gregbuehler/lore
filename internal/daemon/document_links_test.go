package daemon

import (
	"reflect"
	"testing"
)

func TestResolveDocumentLinkUsesSharedLocalSemantics(t *testing.T) {
	d, root, other := documentTestDaemon(t)
	for rel, content := range map[string]string{
		"folder/current.md":        "# Current",
		"folder/Space Note.md":     "---\naliases: [Nickname]\n---\n# Space Note",
		"folder/note.md":           "# Local Note",
		"folder/a(b).md":           "# Parentheses",
		"folder/hash#pipe|.md":     "# Reserved URL Characters",
		"folder/hash%23pipe%7C.md": "# Literal percent sequences",
		"folder/literal.md.md":     "# Literal extension in stem",
		"folder/ spaced .md":       "# Significant spaces",
		"note.md":                  "# Root Note",
		"elsewhere/Target.md":      "# Target",
		"README.md":                "# Readme",
	} {
		writeLinkTestFile(t, root, rel, content)
	}
	writeLinkTestFile(t, other, "only-other.md", "# Other")
	source, err := d.source("vault")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ target, want string }{
		{"Space%20Note.md#heading", "folder/Space Note.md"},
		{"./Space%20Note.md?view=1#heading", "folder/Space Note.md"},
		{"[[folder/Space Note#heading|Label]]", "folder/Space Note.md"},
		{"Space Note#heading|Label", "folder/Space Note.md"},
		{"Nickname", "folder/Space Note.md"},
		{"[[Nickname|Label]]", "folder/Space Note.md"},
		{"note.md", "folder/note.md"},
		{"[[note.md]]", "note.md"},
		{`a\(b\).md`, "folder/a(b).md"},
		{"a%28b%29.md", "folder/a(b).md"},
		{"hash%23pipe%7C.md#heading", "folder/hash#pipe|.md"},
		{"[[folder/hash%23pipe%7C#heading|Alias]]", "folder/hash#pipe|.md"},
		{"[[folder/hash%2523pipe%257C#heading|Alias]]", "folder/hash%23pipe%7C.md"},
		{"[[folder/literal.md.md]]", "folder/literal.md.md"},
		{"[[folder/literal%2Emd]]", "folder/literal.md.md"},
		{"[[folder/%20spaced%20#heading|Alias]]", "folder/ spaced .md"},
		{"../elsewhere/Target.md", "elsewhere/Target.md"},
		{"../README.md", "README.md"},
		{"#heading", ""},
		{"https://example.com/note.md", ""},
		{"mailto:someone@example.com", ""},
		{"only-other", ""},
		{"./missing.md", ""},
	} {
		t.Run(test.target, func(t *testing.T) {
			got, err := resolveDocumentLink(source, "folder/current.md", test.target)
			if err != nil || got != test.want {
				t.Fatalf("resolve %q = %q, %v; want %q", test.target, got, err, test.want)
			}
		})
	}
	for _, target := range []string{"../../escape.md", "%2e%2e/%2e%2e/escape.md", "/absolute.md", "../.secret.md", "bad%zz.md"} {
		if _, err := resolveDocumentLink(source, "folder/current.md", target); err == nil {
			t.Errorf("accepted unsafe target %q", target)
		}
	}
}

func TestDocumentBacklinksIncludeMarkdownButExcludeCodeAndOtherLibraries(t *testing.T) {
	d, root, other := documentTestDaemon(t)
	files := map[string]string{
		"folder/Space Note.md": "# Target",
		"wiki.md":              "[[folder/Space Note#heading|Alias]] [[folder/Space Note]]",
		"folder/markdown.md":   "[A](Space%20Note.md#heading)\n[again](<Space Note.md> \"Title\")",
		"reference.md":         "[A][ref]\n\n[ref]: folder/Space%20Note.md#heading \"Title\"",
		"code.md":              "`[[folder/Space Note]]`\n`` [a](folder/Space%20Note.md) ``\n````markdown\n[[folder/Space Note]]\n```\n[a](folder/Space%20Note.md)\n````\n~~~markdown\n[[folder/Space Note]]\n~~~\n    [[folder/Space Note]]\n",
		"external.md":          "[A](https://example.com/folder/Space%20Note.md)",
	}
	for rel, content := range files {
		writeLinkTestFile(t, root, rel, content)
	}
	writeLinkTestFile(t, other, "other.md", "[[folder/Space Note]]")
	response := d.dispatchBacklinks(&Request{SourceID: "vault", RelPath: "folder/Space Note.md"})
	if !response.OK {
		t.Fatalf("backlinks = %#v", response)
	}
	var paths []string
	for _, doc := range response.Documents {
		paths = append(paths, doc.RelPath)
		if doc.SourceID != "vault" || doc.DocumentID == "" || doc.Revision == "" {
			t.Fatalf("incomplete source-local summary: %#v", doc)
		}
	}
	want := []string{"folder/markdown.md", "reference.md", "wiki.md"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("backlinks = %v, want %v", paths, want)
	}
}

func TestMarkdownDocumentLinksPreserveCodeBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, text string
		want       []string
	}{
		{"inline", "`[[code]]` [[real]]", []string{"real"}},
		{"multiple backticks", "`` ` [[code]] ` `` [[real]]", []string{"real"}},
		{"unmatched backtick", "` [[real]]", []string{"real"}},
		{"fence lengths", "````md\n[[code]]\n```\n[[still-code]]\n````\n[[real]]", []string{"real"}},
		{"tilde fence", "~~~md\n[a](code.md)\n~~~\n[a](real.md)", []string{"real.md"}},
		{"unclosed fence", "[[real]]\n```\n[[code]]", []string{"real"}},
		{"escaped opener", "\\[[code]] [[real]]", []string{"real"}},
		{"escaped backtick", "\\` [[real]]", []string{"real"}},
		{"indented", "    [[code]]\n\t[[code]]\n[[real]]", []string{"real"}},
		{"nested destination", "[a](folder/a(b).md \"title\")", []string{"folder/a(b).md"}},
		{"angle destination", "[a](<folder/a b.md#heading> 'title')", []string{"folder/a b.md#heading"}},
		{"reference", "[a][ref]\n[ref]: folder/real.md 'title'", []string{"folder/real.md"}},
		{"quoted fence", "> ```md\n> [[code]]\n> ```\n[[real]]", []string{"real"}},
		{"list fence", "- ~~~md\n  [[code]]\n  ~~~\n[[real]]", []string{"real"}},
		{"ordered list fence", "1. ~~~\n   [[code]]\n   ~~~\n\n[[real]]", []string{"real"}},
		{"parenthesized list fence", "1) ~~~\n   [[code]]\n   ~~~\n\n[[real]]", []string{"real"}},
		{"long ordered list fence", "123456789. ~~~\n           [[code]]\n           ~~~\n\n[[real]]", []string{"real"}},
		{"quoted ordered list fence", "> 12) ~~~\n>     [[code]]\n>     ~~~\n\n[[real]]", []string{"real"}},
		{"unmatched bracket", "not a link](fake.md) [[real]]", []string{"real"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var got []string
			for _, span := range markdownDocumentLinks(test.text) {
				got = append(got, test.text[span.start:span.end])
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("links = %q, want %q", got, test.want)
			}
		})
	}
}

func TestMoveRewriteRebasesUnresolvedRelativeLinksWithoutChangingExternalLinks(t *testing.T) {
	d, root, _ := documentTestDaemon(t)
	content := "[missing](missing.md)\n![asset](../assets/picture%20one.png#view)\n![reserved](../assets/hash%23pipe%7C%3F%25.png#view)\n[external](https://example.com/a.md)\n[anchor](#heading)\n[[#heading]]\n"
	writeLinkTestFile(t, root, "old/note.md", content)
	source, err := d.source("vault")
	if err != nil {
		t.Fatal(err)
	}
	index, err := scanDocumentLinks(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range index.docs {
		if file.rel != "old/note.md" {
			continue
		}
		got := string(rewriteDocumentLinks(index, file, "old/note.md", "new/deep/note.md"))
		want := "[missing](../../old/missing.md)\n![asset](../../assets/picture%20one.png#view)\n![reserved](../../assets/hash%23pipe%7C%3F%25.png#view)\n[external](https://example.com/a.md)\n[anchor](#heading)\n[[#heading]]\n"
		if got != want {
			t.Fatalf("rewrite = %q, want %q", got, want)
		}
		return
	}
	t.Fatal("source document missing from snapshot")
}
