package hosted

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/gruesomeparty/marginalia/internal/document"
)

// Assets (issue #217) are the pictures a document shows: a design doc's
// screenshots, a mockup. An agent publishes them beside the documents; a
// markdown image or link naming one is pointed at the review's own copy, so
// the page still loads nothing from outside.
//
// An asset is a blob like a document revision — written once, put-if-absent,
// never edited — under its own prefix, and a republish that does not resend
// one keeps serving the copy an earlier revision stored. The bytes are held
// in memory from the publish (or startup) on, so serving one costs the store
// nothing, the same rule every other page follows.

// Asset is one binary file published beside the documents.
type Asset struct {
	Name    string
	Type    string // optional; read from the extension when empty
	Content []byte
}

// AssetMeta is what the metadata records about an asset: which revision
// stored its bytes, its media type and its size.
type AssetMeta struct {
	Rev   int    `json:"rev"`
	Type  string `json:"type"`
	Bytes int64  `json:"bytes"`
}

// assetTypes is every media type an asset may have, by extension. An
// explicit list: an asset is shown inline on a reviewer's page, and a type
// the browser would run or render as a document has no place here.
var assetTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
}

func assetKey(id string, n int, name string) string {
	return prefixReviews + id + "/asset/" + strconv.Itoa(n) + "/" + name
}

// assetURL is where the page finds an asset. The revision is in the path,
// so the URL names one immutable blob and can be cached for good.
func assetURL(id string, a AssetMeta, name string) string {
	return "/r/" + id + "/assets/" + strconv.Itoa(a.Rev) + "/" + url.PathEscape(name)
}

// checkAsset is the rule for one asset: a plain name, a supported type that
// matches its extension, content that is what it says it is.
func checkAsset(a Asset) (string, error) {
	if err := CheckName(a.Name); err != nil {
		return "", fmt.Errorf("asset: %w", err)
	}
	want, ok := assetTypes[strings.ToLower(path.Ext(a.Name))]
	if !ok {
		return "", fmt.Errorf("asset %q: only PNG, JPEG, WebP and SVG images can be published (by extension: .png, .jpg, .jpeg, .webp, .svg)", a.Name)
	}
	if a.Type != "" && a.Type != want {
		return "", fmt.Errorf("asset %q is named as %s but sent as %s", a.Name, want, a.Type)
	}
	if len(a.Content) == 0 {
		return "", fmt.Errorf("asset %q is empty", a.Name)
	}
	if want == "image/svg+xml" {
		if !bytes.Contains(a.Content[:min(len(a.Content), 1024)], []byte("<svg")) {
			return "", fmt.Errorf("asset %q does not look like an SVG image", a.Name)
		}
	} else if got := http.DetectContentType(a.Content); got != want {
		return "", fmt.Errorf("asset %q is named as %s but its content reads as %s", a.Name, want, got)
	}
	return want, nil
}

// linker resolves a document's image and link destinations against the
// review's assets: a relative destination naming one ("mock.png",
// "./mock.png") points at the review's copy, anything else is left alone.
func linker(id string, assets map[string]AssetMeta) document.Options {
	if len(assets) == 0 {
		return document.Options{}
	}
	return document.Options{Link: func(dest string) (string, bool) {
		name, err := url.PathUnescape(strings.TrimPrefix(dest, "./"))
		if err != nil {
			return "", false
		}
		a, ok := assets[name]
		if !ok {
			return "", false
		}
		return assetURL(id, a, name), true
	}}
}

// assetCSP is the policy an asset is served under: nothing in it runs, and
// opened on its own an SVG is sandboxed — an image is shown, never executed.
const assetCSP = "default-src 'none'; style-src 'unsafe-inline'; sandbox"

// handleAsset serves one asset of the current revision from memory.
func (h *Host) handleAsset(w http.ResponseWriter, r *http.Request) {
	rv := h.Review(r.PathValue("id"))
	if rv == nil {
		http.NotFound(w, r)
		return
	}
	name := r.PathValue("name")
	a, ok := rv.Meta.Assets[name]
	if !ok || strconv.Itoa(a.Rev) != r.PathValue("rev") {
		http.NotFound(w, r)
		return
	}
	body := rv.assets[name]
	w.Header().Set("Content-Type", a.Type)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", assetCSP)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}
