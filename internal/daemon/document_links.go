package daemon

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/gregbuehler/lore/internal/parse"
	"github.com/gregbuehler/lore/internal/resolve"
)

// documentLink retains byte offsets so rewriting never reserializes Markdown.
// Wiki targets include their heading/alias; Markdown targets exclude their title.
type documentLink struct {
	start, end int
	wiki       bool
}

type linkedDocument struct {
	rel     string
	path    string
	content []byte
	mode    os.FileMode
}

type documentLinkIndex struct {
	source   sourceRoot
	docs     []linkedDocument
	resolver *resolve.Resolver
}

func scanDocumentLinks(source sourceRoot) (*documentLinkIndex, error) {
	index := make(map[string][]string)
	out := &documentLinkIndex{source: source}
	err := filepath.WalkDir(source.root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source.root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() {
			if rel != "." && strings.HasPrefix(entry.Name(), ".") && rel != ".lore" {
				return filepath.SkipDir
			}
			if strings.HasPrefix(rel, ".lore/") && rel != ".lore/skills" && !strings.HasPrefix(rel, ".lore/skills/") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".md") || (strings.HasPrefix(rel, ".lore/") && !isAllowedLoreDocument(rel)) {
			return nil
		}
		path, rel, err = safeDocumentPath(source.root, rel, false)
		if err != nil {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		out.docs = append(out.docs, linkedDocument{rel: rel, path: path, content: content, mode: info.Mode().Perm()})
		key := strings.TrimSuffix(rel, ".md")
		name := strings.ToLower(filepath.Base(key))
		index[name] = append(index[name], key)
		// Frontmatter aliases complement display aliases (the | suffix).
		aliases := parse.ParseFrontmatterMap(string(content))["aliases"]
		addAlias := func(alias string) {
			alias = strings.ToLower(strings.TrimSpace(alias))
			if alias != "" && alias != name {
				index[alias] = append(index[alias], key)
			}
		}
		switch values := aliases.(type) {
		case string:
			addAlias(values)
		case []any:
			for _, value := range values {
				if alias, ok := value.(string); ok {
					addAlias(alias)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out.resolver = resolve.NewWithIndex(index)
	return out, nil
}

// resolveDocumentLink is shared by navigation, backlinks, and move rewriting.
// An explicit [[wiki]] wrapper disambiguates root-relative wiki paths from
// Markdown destinations, whose .md paths are relative to the source document.
func resolveDocumentLink(source sourceRoot, fromRel, target string) (string, error) {
	index, err := scanDocumentLinks(source)
	if err != nil {
		return "", err
	}
	return index.resolve(fromRel, target)
}

func (index *documentLinkIndex) resolve(fromRel, target string) (string, error) {
	target = strings.TrimSpace(target)
	wiki := strings.HasPrefix(target, "[[") && strings.HasSuffix(target, "]]")
	if wiki {
		target = target[2 : len(target)-2]
	}
	if i := strings.IndexByte(target, '|'); i >= 0 {
		wiki = true
		target = strings.TrimSuffix(target[:i], `\`)
	}
	target = strings.TrimSpace(target)
	if strings.HasPrefix(target, "<") && strings.HasSuffix(target, ">") {
		target = target[1 : len(target)-1]
	}
	if !wiki {
		target = markdownPathUnescaper.Replace(target)
	}
	if i := strings.IndexAny(target, "#?"); i >= 0 {
		target = target[:i]
	}
	if target == "" {
		return "", nil
	}
	if !wiki {
		parsed, err := url.Parse(target)
		if err != nil {
			return "", err
		}
		if parsed.IsAbs() || parsed.Host != "" || strings.HasPrefix(target, "//") {
			return "", nil
		}
	} else {
		// Strip only a literal extension before decoding. Encoded characters
		// belong to the filename, never to wiki suffix syntax.
		target = strings.TrimSuffix(target, ".md")
	}
	var err error
	target, err = url.PathUnescape(target)
	if err != nil {
		return "", err
	}
	if strings.Contains(target, `\`) || strings.HasPrefix(target, "/") {
		return "", fmt.Errorf("link escapes source root")
	}
	markdown := !wiki && strings.HasSuffix(target, ".md")
	if !wiki && filepath.Ext(target) != "" && !markdown && !strings.HasPrefix(target, ".") {
		return "", nil
	}
	if markdown {
		// Decode after splitting the URL suffix, then validate the resulting
		// filesystem path directly: an encoded # or | is part of a filename,
		// not a second heading/display separator.
		candidate := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(fromRel), target)))
		_, rel, err := safeDocumentPath(index.source.root, candidate, true)
		if err != nil {
			return "", err
		}
		if !index.resolver.Exists(strings.TrimSuffix(rel, ".md")) {
			return "", nil
		}
		return rel, nil
	}
	key, relative, err := canonicalLinkTarget(target, strings.TrimSuffix(fromRel, ".md"))
	if err != nil {
		return "", err
	}
	candidate := key
	if !index.resolver.Exists(key) {
		if relative {
			return "", nil
		}
		candidate = index.resolver.Resolve(key, strings.TrimSuffix(fromRel, ".md"))
	}
	if candidate == "" {
		return "", nil
	}
	_, rel, err := safeDocumentPath(index.source.root, filepath.ToSlash(candidate)+".md", false)
	if err != nil {
		return "", nil
	}
	return rel, nil
}

// canonicalLinkTarget validates an already-decoded, extensionless wiki path.
// Suffix parsing and URL decoding happen exactly once, in resolve.
func canonicalLinkTarget(target, fromRel string) (string, bool, error) {
	if target == "" || filepath.IsAbs(target) || strings.Contains(target, `\`) || strings.HasPrefix(target, "/") {
		return "", false, fmt.Errorf("invalid wikilink target")
	}
	relative := false
	for _, part := range strings.Split(filepath.ToSlash(target), "/") {
		if part == "." || part == ".." {
			relative = true
			continue
		}
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, ".") {
			return "", false, fmt.Errorf("invalid wikilink target")
		}
	}
	targetPath := filepath.FromSlash(target)
	if relative {
		targetPath = filepath.Join(filepath.Dir(filepath.FromSlash(fromRel)), targetPath)
	}
	targetPath = filepath.Clean(targetPath)
	if filepath.IsAbs(targetPath) || targetPath == ".." || strings.HasPrefix(targetPath, ".."+string(filepath.Separator)) {
		return "", false, fmt.Errorf("wikilink target escapes source root")
	}
	target = filepath.ToSlash(targetPath)
	for _, part := range strings.Split(target, "/") {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, ".") {
			return "", false, fmt.Errorf("invalid wikilink target")
		}
	}
	return target, relative, nil
}

var markdownPathUnescaper = strings.NewReplacer(`\(`, "(", `\)`, ")", `\[`, "[", `\]`, "]")
var markdownPathEscaper = strings.NewReplacer("(", "%28", ")", "%29", ":", "%3A")

func (d *Daemon) dispatchBacklinks(req *Request) *Response {
	d.state.mu.RLock()
	defer d.state.mu.RUnlock()
	source, err := d.source(req.SourceID)
	if err != nil {
		return sourceError(err)
	}
	path, rel, err := safeDocumentPath(source.root, req.RelPath, false)
	if err != nil {
		return pathError(err)
	}
	if _, err := os.Stat(path); err != nil {
		return &Response{OK: false, Code: "not_found", Error: "document not found"}
	}
	index, err := scanDocumentLinks(source)
	if err != nil {
		return &Response{OK: false, Error: "unable to read source documents"}
	}
	out := make([]DocumentSummary, 0)
	for _, file := range index.docs {
		if file.rel == rel {
			continue
		}
		text := string(file.content)
		for _, link := range markdownDocumentLinks(text) {
			target := text[link.start:link.end]
			if link.wiki {
				target = "[[" + target + "]]"
			}
			resolved, err := index.resolve(file.rel, target)
			if err != nil || resolved != rel {
				continue
			}
			doc, err := d.document(source, file.rel, file.content)
			if err != nil {
				return &Response{OK: false, Error: "unable to read document identity"}
			}
			out = append(out, summaryOf(doc))
			break
		}
	}
	return &Response{OK: true, Documents: out}
}

// markdownDocumentLinks scans destination spans, including reference definitions.
// Fenced blocks and matched backtick spans are skipped before looking for links.
func markdownDocumentLinks(text string) []documentLink {
	var links []documentLink
	var fence byte
	fenceSize := 0
	bracketDepth := 0
	for i := 0; i < len(text); {
		if i == 0 || text[i-1] == '\n' {
			end := strings.IndexByte(text[i:], '\n')
			if end < 0 {
				end = len(text) - i
			}
			end += i
			start := i
			for start < end && text[start] == ' ' {
				start++
			}
			indent := start - i
			// Container prefixes are not code: fences inside blockquotes and
			// list items still protect their complete contents from rewrites.
			for start < end && text[start] == '>' {
				start++
				for start < end && text[start] == ' ' {
					start++
				}
			}
			markerEnd := start
			if markerEnd < end && strings.ContainsRune("-+*", rune(text[markerEnd])) {
				markerEnd++
			} else {
				// CommonMark ordered markers contain one to nine digits and
				// either delimiter; their content follows the same fence rules.
				for markerEnd < end && markerEnd-start < 9 && text[markerEnd] >= '0' && text[markerEnd] <= '9' {
					markerEnd++
				}
				if markerEnd > start && markerEnd < end && (text[markerEnd] == '.' || text[markerEnd] == ')') {
					markerEnd++
				} else {
					markerEnd = start
				}
			}
			if markerEnd > start && markerEnd < end && (text[markerEnd] == ' ' || text[markerEnd] == '\t') {
				start = markerEnd + 1
				for start < end && (text[start] == ' ' || text[start] == '\t') {
					start++
				}
			}
			if start < end && (indent <= 3 || fence != 0) && (text[start] == '`' || text[start] == '~') {
				n := repeatedByte(text, start)
				if fence != 0 {
					if text[start] == fence && n >= fenceSize && strings.TrimSpace(text[start+n:end]) == "" {
						fence = 0
					}
					i = end
					if i < len(text) {
						i++
					}
					continue
				}
				if n >= 3 {
					fence, fenceSize = text[start], n
					i = end
					if i < len(text) {
						i++
					}
					continue
				}
			}
			if fence != 0 || indent >= 4 || (start < end && text[start] == '\t') {
				i = end
				if i < len(text) {
					i++
				}
				continue
			}
		}
		if text[i] == '\\' && i+1 < len(text) {
			i += 2
			continue
		}
		if text[i] == '`' {
			n := repeatedByte(text, i)
			found := false
			for j := i + n; j < len(text); {
				if text[j] != '`' {
					j++
					continue
				}
				m := repeatedByte(text, j)
				if m == n {
					i, found = j+m, true
					break
				}
				j += m
			}
			if !found {
				i += n
			}
			continue
		}
		if strings.HasPrefix(text[i:], "[[") {
			if n := strings.Index(text[i+2:], "]]"); n >= 0 && !strings.Contains(text[i+2:i+2+n], "\n") {
				links = append(links, documentLink{start: i + 2, end: i + 2 + n, wiki: true})
				i += n + 4
				continue
			}
		}
		if text[i] == '[' {
			bracketDepth++
		}
		if text[i] == ']' {
			if bracketDepth == 0 {
				i++
				continue
			}
			bracketDepth--
		}
		if text[i] == ']' && i+1 < len(text) && (text[i+1] == '(' || text[i+1] == ':') {
			reference := text[i+1] == ':'
			if reference {
				lineStart := strings.LastIndexByte(text[:i], '\n') + 1
				prefix := strings.TrimLeft(text[lineStart:i], " ")
				if !strings.HasPrefix(prefix, "[") || strings.Contains(prefix[1:], "]") {
					i++
					continue
				}
			}
			start := i + 2
			for start < len(text) && (text[start] == ' ' || text[start] == '\t' || (!reference && text[start] == '\n')) {
				start++
			}
			end, next, ok := markdownDestination(text, start, reference)
			if ok {
				if text[start] == '<' {
					start++
				}
				links = append(links, documentLink{start: start, end: end})
				i = next
				continue
			}
		}
		i++
	}
	return links
}

func repeatedByte(text string, start int) int {
	end := start + 1
	for end < len(text) && text[end] == text[start] {
		end++
	}
	return end - start
}

func markdownDestination(text string, start int, reference bool) (int, int, bool) {
	if start >= len(text) {
		return 0, 0, false
	}
	end := start
	if text[start] == '<' {
		end++
		for end < len(text) && text[end] != '>' && text[end] != '\n' {
			if text[end] == '\\' && end+1 < len(text) {
				end++
			}
			end++
		}
		if end >= len(text) || text[end] != '>' {
			return 0, 0, false
		}
	} else {
		depth := 0
		for end < len(text) {
			c := text[end]
			if c == '\\' && end+1 < len(text) {
				end += 2
				continue
			}
			if c == '(' {
				depth++
			} else if c == ')' {
				if depth == 0 {
					break
				}
				depth--
			} else if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
				break
			}
			end++
		}
		if depth != 0 || end == start {
			return 0, 0, false
		}
	}
	next := end
	if text[start] == '<' {
		next++
	}
	if reference {
		return end, next, true
	}
	for next < len(text) && strings.ContainsRune(" \t\r\n", rune(text[next])) {
		next++
	}
	if next < len(text) && (text[next] == '\'' || text[next] == '"' || text[next] == '(') {
		close := text[next]
		if close == '(' {
			close = ')'
		}
		next++
		for next < len(text) && text[next] != close {
			if text[next] == '\\' && next+1 < len(text) {
				next++
			}
			next++
		}
		if next == len(text) {
			return 0, 0, false
		}
		next++
		for next < len(text) && strings.ContainsRune(" \t\r\n", rune(text[next])) {
			next++
		}
	}
	return end, next + 1, next < len(text) && text[next] == ')'
}
