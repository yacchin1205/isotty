package isotty

import (
	"path"
	"strings"
)

// syncIgnored reports whether the sync leaves a project-relative path on
// the host, by the default ignores: a pattern with a leading slash is
// anchored at the project, one with a trailing slash names a directory and
// everything under it, and one without a slash matches a name at any depth.
func syncIgnored(rel string) bool {
	components := strings.Split(strings.Trim(path.Clean("/"+rel), "/"), "/")
	for _, pattern := range defaultIgnores {
		anchored := strings.HasPrefix(pattern, "/")
		directory := strings.HasSuffix(pattern, "/")
		core := strings.Trim(pattern, "/")
		last := len(components)
		if directory {
			// The file itself is never a directory; its ancestors may be.
			last--
		}
		for i := 1; i <= last; i++ {
			prefix := strings.Join(components[:i], "/")
			if anchored {
				if ok, _ := path.Match(core, prefix); ok {
					return true
				}
				continue
			}
			if ok, _ := path.Match(core, components[i-1]); ok {
				return true
			}
			if strings.Contains(core, "/") && strings.HasSuffix("/"+prefix, "/"+core) {
				return true
			}
		}
	}
	return false
}
