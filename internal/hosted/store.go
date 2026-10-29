package hosted

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gruesomeparty/marginalia/internal/session"
	"github.com/gruesomeparty/marginalia/internal/storage"
)

// DefaultStoreURI is where `host` keeps state when nothing else is said: a
// directory beside wherever it was started.
const DefaultStoreURI = "file://./marginalia-data"

// The R2 credentials come from the environment and nowhere else: a flag
// shows in process lists and logs, and so would the key.
const (
	R2AccountEnv = "MARGINALIA_R2_ACCOUNT_ID"
	R2KeyEnv     = "MARGINALIA_R2_ACCESS_KEY_ID"
	R2SecretEnv  = "MARGINALIA_R2_SECRET_ACCESS_KEY" //nolint:gosec // the name of an environment variable, not a credential
	// R2EndpointEnv overrides the endpoint derived from the account id —
	// for an S3-compatible store that is not R2, or a local fake.
	R2EndpointEnv = "MARGINALIA_R2_ENDPOINT"
)

// OpenStore resolves a --store URI into a backend and a description safe to
// print. `file://<path>` is the disk backend rooted at path, absolute
// (`file:///data`) or relative to the working directory
// (`file://./marginalia-data`). `r2://<bucket>/<prefix>` is Cloudflare R2
// through its S3 API, with the credentials from the environment. Any other
// scheme is a backend that does not exist yet, which is a feature request
// rather than a typo.
func OpenStore(uri string) (storage.Store, string, error) {
	scheme, rest, ok := strings.Cut(uri, "://")
	if !ok {
		return nil, "", session.Advertise(fmt.Errorf("--store %q is not a URI — use file://<path> or r2://<bucket>/<prefix>", uri))
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
	case "r2":
		bucket, prefix, _ := strings.Cut(rest, "/")
		if bucket == "" {
			return nil, "", fmt.Errorf("--store %q names no bucket — use r2://<bucket>/<prefix>", uri)
		}
		cfg := storage.R2Config{
			AccountID:       os.Getenv(R2AccountEnv),
			AccessKeyID:     os.Getenv(R2KeyEnv),
			SecretAccessKey: os.Getenv(R2SecretEnv),
			Bucket:          bucket,
			Prefix:          prefix,
			Endpoint:        os.Getenv(R2EndpointEnv),
		}
		if cfg.AccountID == "" && cfg.Endpoint == "" {
			return nil, "", fmt.Errorf("--store r2:// needs %s (or %s) in the environment", R2AccountEnv, R2EndpointEnv)
		}
		if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
			return nil, "", fmt.Errorf("--store r2:// needs %s and %s in the environment — never on the command line", R2KeyEnv, R2SecretEnv)
		}
		store, err := storage.NewR2(cfg)
		if err != nil {
			return nil, "", err
		}
		where := "r2://" + bucket
		if prefix != "" {
			where += "/" + strings.Trim(prefix, "/")
		}
		return store, where, nil
	}
	return nil, "", session.Advertise(fmt.Errorf("--store scheme %q is not supported yet — available: file://, r2://", scheme))
}
