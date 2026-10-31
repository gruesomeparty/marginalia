package hosted

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gruesomeparty/marginalia/internal/storage"
	"github.com/gruesomeparty/marginalia/internal/storage/storagetest"
)

func pngOf(t *testing.T, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, c)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func fetch(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(res.Body)
	return res, buf.Bytes()
}

// The acceptance criterion of issue #217: plan.md links mock.png, published
// together; the page shows the review's own copy; a republish without the
// asset keeps serving the earlier revision's; an asset over the cap is a
// limit error; and after a restart serving one reads nothing.
func TestPublishedAssetsResolveAndSurviveARepublish(t *testing.T) {
	counting := storagetest.NewCounting(storage.NewMemory())
	h, ts := open(t, counting)
	ctx := context.Background()
	plan := Document{Name: "plan.md", Content: []byte("# Plan\n\n![mock](mock.png) and [full size](./mock.png), not ![x](https://example.com/x.png).\n")}
	mock := pngOf(t, color.Black)
	r, err := h.Publish(ctx, Publish{Agent: "a", Docs: []Document{plan}, Assets: []Asset{{Name: "mock.png", Content: mock}}})
	if err != nil {
		t.Fatal(err)
	}
	id := r.Meta.ID
	want := "/r/" + id + "/assets/1/mock.png"
	res, page := fetch(t, ts.URL+"/r/"+id+"/")
	if strings.Count(string(page), want) != 2 || !strings.Contains(string(page), "https://example.com/x.png") {
		t.Errorf("page does not point the image and link at the asset (want %s)", want)
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "img-src 'self' data:") {
		t.Errorf("review page CSP %q does not keep images on this host", csp)
	}
	res, body := fetch(t, ts.URL+want)
	if res.StatusCode != 200 || !bytes.Equal(body, mock) || res.Header.Get("Content-Type") != "image/png" ||
		res.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(res.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("asset: %d %v", res.StatusCode, res.Header)
	}

	// A republish that does not resend the asset keeps it, at the same URL.
	if _, err := h.Republish(ctx, id, Publish{Docs: []Document{plan}}); err != nil {
		t.Fatal(err)
	}
	if _, page = fetch(t, ts.URL+"/r/"+id+"/"); !strings.Contains(string(page), want) {
		t.Error("revision 2 lost the image it did not resend")
	}
	if res, body = fetch(t, ts.URL+want); res.StatusCode != 200 || !bytes.Equal(body, mock) {
		t.Errorf("revision 1's asset after a republish: %d", res.StatusCode)
	}
	// Resending it stores revision 3's copy; revision 1's blob is untouched.
	other := pngOf(t, color.White)
	if _, err := h.Republish(ctx, id, Publish{Docs: []Document{plan}, Assets: []Asset{{Name: "mock.png", Type: "image/png", Content: other}}}); err != nil {
		t.Fatal(err)
	}
	if res, body = fetch(t, ts.URL+"/r/"+id+"/assets/3/mock.png"); res.StatusCode != 200 || !bytes.Equal(body, other) {
		t.Errorf("revision 3's asset: %d", res.StatusCode)
	}
	if res, _ = fetch(t, ts.URL+want); res.StatusCode != http.StatusNotFound {
		t.Errorf("a replaced asset's old URL: %d, want 404", res.StatusCode)
	}
	if old, err := counting.GetBlob(ctx, assetKey(id, 1, "mock.png")); err != nil || !bytes.Equal(old, mock) {
		t.Errorf("revision 1's blob changed: %v", err)
	}

	// A fresh process serves it from memory.
	h2, err := Open(ctx, Options{Store: counting, PublicURL: "https://review.example", Token: "tok", DevIdentity: "dev@example.com", Log: testWriter{t}})
	if err != nil {
		t.Fatal(err)
	}
	ts2 := httptest.NewServer(h2.Handler())
	t.Cleanup(ts2.Close)
	counting.Reset()
	if res, body = fetch(t, ts2.URL+"/r/"+id+"/assets/3/mock.png"); res.StatusCode != 200 || !bytes.Equal(body, other) {
		t.Errorf("after a restart: %d", res.StatusCode)
	}
	if got := counting.Reads(); got != 0 {
		t.Errorf("serving an asset cost %d store reads, want 0", got)
	}
}

func TestAssetsAreRefusedBeforeAnythingIsWritten(t *testing.T) {
	counting := storagetest.NewCounting(storage.NewMemory())
	h, err := Open(context.Background(), Options{Store: counting, PublicURL: "https://review.example", DevIdentity: "dev@example.com", Log: testWriter{t}, Limits: Limits{AssetBytes: 64}})
	if err != nil {
		t.Fatal(err)
	}
	doc := []Document{{Name: "plan.md", Content: []byte("# Plan\n")}}
	big := pngOf(t, color.Black)
	counting.Reset()
	_, err = h.Publish(context.Background(), Publish{Docs: doc, Assets: []Asset{{Name: "mock.png", Content: append(big, make([]byte, 64)...)}}})
	var limit *LimitError
	if !errors.As(err, &limit) || limit.Limit != "bytes per asset" {
		t.Errorf("an asset over the cap: %v, want the bytes-per-asset limit", err)
	}
	for name, a := range map[string]Asset{
		"not an image":  {Name: "notes.txt", Content: []byte("hi")},
		"mislabelled":   {Name: "mock.png", Content: []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")},
		"wrong type":    {Name: "mock.png", Type: "image/jpeg", Content: big},
		"not an svg":    {Name: "a.svg", Content: []byte("hello")},
		"nested name":   {Name: "img/mock.png", Content: big},
		"empty content": {Name: "mock.png"},
	} {
		if _, err := h.Publish(context.Background(), Publish{Docs: doc, Assets: []Asset{a}}); err == nil {
			t.Errorf("%s: published", name)
		}
	}
	if _, err := h.Publish(context.Background(), Publish{Docs: doc, Assets: []Asset{{Name: "a.png", Content: big}, {Name: "a.png", Content: big}}}); err == nil {
		t.Error("a duplicate asset name was published")
	}
	if got := counting.Writes(); got != 0 {
		t.Errorf("refused publishes wrote %d time(s)", got)
	}
}

// publish_document carries assets as base64, and refuses what is not.
func TestPublishDocumentCarriesAssets(t *testing.T) {
	h, ts := open(t, storage.NewMemory())
	cs := connect(t, ts)
	mock := pngOf(t, color.Black)
	var out publishOut
	if err := call(t, cs, "publish_document", map[string]any{
		"documents": []map[string]any{{"name": "plan.md", "content": "# Plan\n\n![mock](mock.png)\n"}},
		"assets":    []map[string]any{{"name": "mock.png", "content_base64": base64.StdEncoding.EncodeToString(mock)}},
	}, &out); err != nil {
		t.Fatal(err)
	}
	if a := h.Review(out.ReviewID).Meta.Assets["mock.png"]; a.Rev != 1 || a.Type != "image/png" || a.Bytes != int64(len(mock)) {
		t.Errorf("asset metadata %+v", a)
	}
	if err := call(t, cs, "publish_document", map[string]any{
		"documents": []map[string]any{{"name": "plan.md", "content": "# Plan\n"}},
		"assets":    []map[string]any{{"name": "mock.png", "content_base64": "not base64!"}},
	}, &out); err == nil {
		t.Error("an asset that is not base64 was published")
	}
}
