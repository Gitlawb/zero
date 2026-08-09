package terminalpet

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestCatalogPreviewInstallAndOfflineReload(t *testing.T) {
	preview := encodedPNG(t, 24*previewFrameCount, 26)
	atlas := encodedPNG(t, 24*atlasColumns, 26*11)
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/manifest":
			manifest := compactManifest{
				Version:   2,
				AssetBase: server.URL,
				Fields:    []string{"slug", "displayName", "kind", "submittedBy", "spritesheet", "petJson", "zip", "spriteVersionNumber"},
			}
			row := []any{"boba", "Boba", "animal", "tester", "pets/boba/sprite.webp", "pets/boba/petjson.json", "pets/boba/archive.zip", 2}
			encoded, _ := json.Marshal(row)
			manifest.Pets = []json.RawMessage{encoded}
			_ = json.NewEncoder(writer).Encode(manifest)
		case "/pets/boba/preview.webp":
			_, _ = writer.Write(preview)
		case "/pets/boba/sprite.webp":
			_, _ = writer.Write(atlas)
		case "/pets/boba/petjson.json":
			_, _ = writer.Write([]byte(`{"displayName":"Boba","description":"A calm blue-screen gremlin."}`))
		case "/ranking":
			_, _ = writer.Write([]byte(`{"pets":[{"slug":"boba"}],"nextCursor":null}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	client := testClient(t, root, server)
	entries, err := client.Catalog(context.Background())
	if err != nil || len(entries) != 1 {
		t.Fatalf("Catalog() = %#v, %v", entries, err)
	}
	entry := entries[0]
	if entry.Slug != "boba" || entry.SpriteVersion != 2 || entry.AssetBase != server.URL {
		t.Fatalf("unexpected catalog entry: %#v", entry)
	}
	if entry.SpritesheetURL != server.URL+"/pets/boba/sprite.webp" {
		t.Fatalf("relative spritesheet was not resolved: %q", entry.SpritesheetURL)
	}
	previewAnimation, err := client.Preview(context.Background(), entry)
	if err != nil || previewAnimation.Frame(Idle, 5) == nil {
		t.Fatalf("Preview() animation=%v err=%v", previewAnimation, err)
	}
	installed, err := client.Install(context.Background(), entry)
	if err != nil || installed.Frame(Running, 3) == nil {
		t.Fatalf("Install() animation=%v err=%v", installed, err)
	}
	if _, err := os.Stat(filepath.Join(root, "pets", "installed", "boba", "source.json")); err != nil {
		t.Fatalf("installed source marker: %v", err)
	}
	server.Close()
	offlineEntries, err := client.Catalog(context.Background())
	if err != nil || len(offlineEntries) != 1 || !offlineEntries[0].Local {
		t.Fatalf("offline Catalog() = %#v, %v", offlineEntries, err)
	}
	if _, err := client.LoadInstalled("boba"); err != nil {
		t.Fatalf("LoadInstalled: %v", err)
	}
}

func TestCatalogRejectsUntrustedAssetHost(t *testing.T) {
	client := NewClient(t.TempDir())
	manifest := compactManifest{
		Version:   2,
		AssetBase: "https://evil.example",
		Fields:    []string{"slug", "displayName", "kind", "submittedBy", "spritesheet", "petJson", "spriteVersionNumber"},
	}
	row, _ := json.Marshal([]any{"boba", "Boba", "animal", nil, "https://evil.example/pets/boba/sprite.webp", "", 2})
	manifest.Pets = []json.RawMessage{row}
	data, _ := json.Marshal(manifest)
	if _, err := client.decodeCatalog(data); err == nil {
		t.Fatal("decodeCatalog accepted an untrusted asset host")
	}
}

func TestCatalogOrdersRankedPetsBeforeAlphabeticalRemainder(t *testing.T) {
	manifest := compactManifest{
		Version:   2,
		AssetBase: "",
		Fields:    []string{"slug", "displayName", "kind", "submittedBy", "spritesheet", "petJson", "zip", "spriteVersionNumber"},
	}
	for _, row := range [][]any{
		{"alpha", "Alpha", "creature", "tester", "pets/alpha/sprite.webp", "pets/alpha/pet.json", nil, 1},
		{"beta", "Beta", "creature", "tester", "pets/beta/sprite.webp", "pets/beta/pet.json", nil, 1},
		{"zeta", "Zeta", "creature", "tester", "pets/zeta/sprite.webp", "pets/zeta/pet.json", nil, 1},
	} {
		encoded, _ := json.Marshal(row)
		manifest.Pets = append(manifest.Pets, encoded)
	}
	var rankingFails bool
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/manifest":
			manifest.AssetBase = server.URL
			_ = json.NewEncoder(writer).Encode(manifest)
		case "/ranking":
			if rankingFails {
				http.Error(writer, "unavailable", http.StatusServiceUnavailable)
				return
			}
			_, _ = writer.Write([]byte(`{"pets":[{"slug":"zeta"},{"slug":"alpha"}],"nextCursor":60}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := testClient(t, t.TempDir(), server)
	entries, err := client.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := entrySlugs(entries); !slices.Equal(got, []string{"zeta", "alpha", "beta"}) {
		t.Fatalf("ranked catalog = %v", got)
	}

	rankingFails = true
	fallback := testClient(t, t.TempDir(), server)
	entries, err = fallback.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := entrySlugs(entries); !slices.Equal(got, []string{"alpha", "beta", "zeta"}) {
		t.Fatalf("fallback catalog = %v", got)
	}
}

func TestAssetPathAndSlugValidation(t *testing.T) {
	client := NewClient(t.TempDir())
	for _, raw := range []string{
		"https://assets.petdex.dev/other/sprite.webp",
		"https://assets.petdex.dev/pets/%2e%2e/secret",
		"https://user@assets.petdex.dev/pets/boba/sprite.webp",
	} {
		if _, err := client.trustedAssetURL(raw); err == nil {
			t.Errorf("trustedAssetURL(%q) unexpectedly succeeded", raw)
		}
	}
	for _, slug := range []string{"../boba", "/boba", "Boba", "boba/other", ""} {
		if err := validateSlug(slug); err == nil {
			t.Errorf("validateSlug(%q) unexpectedly succeeded", slug)
		}
	}
}

func TestAssetRedirectToUntrustedHostIsRejectedBeforeFollowing(t *testing.T) {
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "https://example.com/pets/boba/sprite.webp", http.StatusFound)
	}))
	defer redirect.Close()
	parsed, _ := url.Parse(redirect.URL)
	client := NewClient(t.TempDir())
	client.HTTPClient = redirect.Client()
	client.TrustedHosts = map[string]bool{parsed.Hostname(): true}
	client.TrustedAssetHosts = map[string]bool{parsed.Hostname(): true}
	if _, err := client.fetchAsset(context.Background(), redirect.URL+"/pets/boba/sprite.webp", maxSpriteBytes); err == nil {
		t.Fatal("fetchAsset followed a redirect to an untrusted host")
	}
}

func TestAtlasAnimationRejectsWrongGeometry(t *testing.T) {
	bad := image.NewNRGBA(image.Rect(0, 0, 192, 285))
	if _, err := AtlasAnimation(bad, 2); err == nil {
		t.Fatal("AtlasAnimation accepted a non-grid image")
	}
	if _, err := AtlasAnimation(image.NewNRGBA(image.Rect(0, 0, 192, 286)), 3); err == nil {
		t.Fatal("AtlasAnimation accepted an unknown sprite version")
	}
}

func TestAtlasAnimationUsesPerStateFrameCounts(t *testing.T) {
	atlas := image.NewNRGBA(image.Rect(0, 0, 192, 26*9))
	for column := 0; column < 6; column++ {
		atlas.SetNRGBA(column*24, 0, color.NRGBA{R: 255, A: 255})
	}
	animation, err := AtlasAnimation(atlas, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Idle has six frames: phase 6 wraps to frame zero instead of entering the
	// two unused atlas columns, which are commonly transparent.
	_, _, _, alpha := animation.Frame(Idle, 6).At(0, 0).RGBA()
	if alpha == 0 {
		t.Fatal("idle phase 6 selected an unused transparent atlas column")
	}
}

func testClient(t *testing.T, root string, server *httptest.Server) *Client {
	t.Helper()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	host := parsed.Hostname()
	client := NewClient(root)
	client.ManifestURL = server.URL + "/manifest"
	client.RankingURL = server.URL + "/ranking"
	client.HTTPClient = server.Client()
	client.TrustedHosts = map[string]bool{host: true}
	client.TrustedAssetHosts = map[string]bool{host: true}
	return client
}

func entrySlugs(entries []Entry) []string {
	result := make([]string, len(entries))
	for index, entry := range entries {
		result[index] = entry.Slug
	}
	return result
}

func encodedPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	value := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			value.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 180, A: 255})
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, value); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
