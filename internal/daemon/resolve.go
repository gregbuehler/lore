package daemon

import (
	"github.com/gregbuehler/lore/internal/resolve"
)

// shortNameResolver wraps resolve.Resolver to preserve the private API
// used by state.go (scan/resolve/exists with lowercase method names).
type shortNameResolver struct {
	r *resolve.Resolver
}

func newShortNameResolver() *shortNameResolver {
	return &shortNameResolver{r: resolve.New()}
}

func (s *shortNameResolver) scan(root string) {
	s.r.Scan(root)
}

func (s *shortNameResolver) resolve(target, sourceRelPath string) string {
	return s.r.Resolve(target, sourceRelPath)
}

func (s *shortNameResolver) exists(relPath string) bool {
	return s.r.Exists(relPath)
}
