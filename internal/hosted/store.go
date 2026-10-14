package hosted

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gruesomeparty/marginalia/internal/session"
	"github.com/gruesomeparty/marginalia/internal/storage"
)

// DefaultStoreURI is where `host` keeps state when nothing else is said: a
// directory beside wherever it was started.
const DefaultStoreURI = "file://./marginalia-data"

// OpenStore resolves a --store URI into a backend. `file://<path>` is the
// disk backend rooted at path, absolute (`file:///data`) or relative to the
// working directory (`file://./marginalia-data`). Any other scheme is a
// backend that does not exist yet, which is a feature request rather than a
// typo — r2:// arrives with #101.
func OpenStore(uri string) (storage.Store, string, error) {
	scheme, rest, ok := strings.Cut(uri, "://")
	if !ok {
		return nil, "", session.Advertise(fmt.Errorf("--store %q is not a URI — use file://<path>", uri))
	}
	switch scheme {
	case "file":
		if rest == "" {
			return nil, "", fmt.Errorf("--store %q names no directory", uri)
		}
		dir, err := filepath.Abs(filepath.FromSlash(rest))
		if err != nil {
			return nil, "", err
		}
		return storage.NewDisk(dir), "file://" + filepath.ToSlash(dir), nil
	}
	return nil, "", session.Advertise(fmt.Errorf("--store scheme %q is not supported yet — available: file://", scheme))
}
