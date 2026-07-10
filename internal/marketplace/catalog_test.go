package marketplace

import (
	"crypto/ed25519"
	"strings"
	"testing"
)

func TestParseCatalogValidatesMarketplaceContract(t *testing.T) {
	catalog := testCatalogJSON()

	parsed, err := ParseCatalog([]byte(catalog))
	if err != nil {
		t.Fatalf("ParseCatalog returned error: %v", err)
	}

	if parsed.SchemaVersion != 1 || parsed.ID != "official" {
		t.Fatalf("unexpected catalog metadata: %#v", parsed)
	}
	if len(parsed.Plugins) != 2 || parsed.Plugins[0].ID != "zero.demo" {
		t.Fatalf("unexpected plugins: %#v", parsed.Plugins)
	}
	release := parsed.Plugins[0].Releases[0]
	if release.Version != "1.2.3" || release.Commit != strings.Repeat("a", 40) {
		t.Fatalf("unexpected release: %#v", release)
	}
	if len(release.Components.Tools) != 1 || release.Components.Tools[0].Permission != "prompt" {
		t.Fatalf("tool inventory not parsed: %#v", release.Components.Tools)
	}
	if len(release.Components.Hooks) != 1 || release.Components.Hooks[0].Event != "beforeTool" {
		t.Fatalf("hook inventory not parsed: %#v", release.Components.Hooks)
	}
}

func TestParseCatalogRejectsDuplicatePluginAndReleaseIDs(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "duplicate plugin id",
			body: strings.Replace(testCatalogJSON(), `"id": "zero.second"`, `"id": "zero.demo"`, 1),
			want: `duplicate plugin id "zero.demo"`,
		},
		{
			name: "duplicate release version",
			body: strings.Replace(testCatalogJSON(), `"version": "1.2.4"`, `"version": "1.2.3"`, 1),
			want: `duplicate release version "1.2.3"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseCatalog([]byte(tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q error, got %v", tc.want, err)
			}
		})
	}
}

func TestParseCatalogRejectsInvalidReleaseAndSpecialistHookEvents(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "invalid semver",
			body: strings.Replace(testCatalogJSON(), `"version": "1.2.3"`, `"version": "latest"`, 1),
			want: "semantic version",
		},
		{
			name: "invalid commit",
			body: strings.Replace(testCatalogJSON(), strings.Repeat("a", 40), "main", 1),
			want: "40-character git commit SHA",
		},
		{
			name: "invalid hash",
			body: strings.Replace(testCatalogJSON(), `sha256:`+strings.Repeat("b", 64), "sha256:nothex", 1),
			want: "sha256:",
		},
		{
			name: "unsafe subdir",
			body: strings.Replace(testCatalogJSON(), `"subdir": "plugins/demo"`, `"subdir": "../escape"`, 1),
			want: "safe relative path",
		},
		{
			name: "specialist hook",
			body: strings.Replace(testCatalogJSON(), `"beforeTool"`, `"specialistStart"`, 1),
			want: `unsupported hook event "specialistStart"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseCatalog([]byte(tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q error, got %v", tc.want, err)
			}
		})
	}
}

func TestParseCatalogRejectsHTTPSDocumentReleaseRepository(t *testing.T) {
	body := strings.Replace(testCatalogJSON(), `"repository": "https://github.com/Gitlawb/zero-demo-plugin.git"`, `"repository": "https://example.com/catalog.json"`, 1)
	_, err := ParseCatalog([]byte(body))
	if err == nil || !strings.Contains(err.Error(), "git repository source") {
		t.Fatalf("expected git repository source error, got %v", err)
	}
}

func TestRemoteCatalogReleaseSourcesRejectLocalPaths(t *testing.T) {
	body := strings.Replace(testCatalogJSON(), `"repository": "https://github.com/Gitlawb/zero-demo-plugin.git"`, `"repository": "./plugins/demo"`, 1)
	catalog, err := ParseCatalog([]byte(body))
	if err != nil {
		t.Fatalf("ParseCatalog returned error: %v", err)
	}
	err = ValidateRemoteCatalogReleaseSources(catalog)
	if err == nil || !strings.Contains(err.Error(), "absolute git repository source") {
		t.Fatalf("expected absolute git source error, got %v", err)
	}
}

func TestRemoteCatalogReleaseSourcesRejectFileURLs(t *testing.T) {
	body := strings.Replace(testCatalogJSON(), `"repository": "https://github.com/Gitlawb/zero-demo-plugin.git"`, `"repository": "file:///tmp/zero-demo-plugin"`, 1)
	catalog, err := ParseCatalog([]byte(body))
	if err != nil {
		t.Fatalf("ParseCatalog returned error: %v", err)
	}
	err = ValidateRemoteCatalogReleaseSources(catalog)
	if err == nil || !strings.Contains(err.Error(), "absolute git repository source") {
		t.Fatalf("expected absolute git source error, got %v", err)
	}
}

func TestParseCatalogRejectsIncompleteReviewMetadata(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "missing review date",
			body: strings.Replace(testCatalogJSON(), `"date": "2026-07-10",`, ``, 1),
			want: "review.date",
		},
		{
			name: "missing reviewer",
			body: strings.Replace(testCatalogJSON(), `"reviewer": "Zero Security",`, ``, 1),
			want: "review.reviewer",
		},
		{
			name: "missing review URL",
			body: strings.Replace(testCatalogJSON(), `"url": "https://github.com/Gitlawb/zero-plugins/pull/1"`, `"url": ""`, 1),
			want: "review.url",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseCatalog([]byte(tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q error, got %v", tc.want, err)
			}
		})
	}
}

func TestVerifyCatalogSignature(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(testCatalogJSON())
	signature := ed25519.Sign(privateKey, data)

	result := VerifyCatalogSignature(data, signature, publicKey)
	if result.Status != VerificationSigned || result.KeyFingerprint == "" {
		t.Fatalf("signed verification failed: %#v", result)
	}

	result = VerifyCatalogSignature(data, nil, publicKey)
	if result.Status != VerificationUnsigned {
		t.Fatalf("unsigned verification = %#v", result)
	}

	result = VerifyCatalogSignature(append(data, '\n'), signature, publicKey)
	if result.Status != VerificationInvalid {
		t.Fatalf("invalid verification = %#v", result)
	}
}

func TestParseCatalogSource(t *testing.T) {
	cases := []struct {
		source string
		kind   CatalogSourceKind
		canon  string
	}{
		{"Gitlawb/zero-plugins", CatalogSourceGitHub, "https://github.com/Gitlawb/zero-plugins.git"},
		{"https://example.com/catalog.json", CatalogSourceHTTPS, "https://example.com/catalog.json"},
		{"git@github.com:Gitlawb/zero-plugins.git", CatalogSourceGit, "git@github.com:Gitlawb/zero-plugins.git"},
		{"./catalog.json", CatalogSourceLocal, "./catalog.json"},
	}
	for _, tc := range cases {
		t.Run(tc.source, func(t *testing.T) {
			parsed, err := ParseCatalogSource(tc.source)
			if err != nil {
				t.Fatalf("ParseCatalogSource: %v", err)
			}
			if parsed.Kind != tc.kind || parsed.Canonical != tc.canon {
				t.Fatalf("source = %#v, want kind=%s canonical=%s", parsed, tc.kind, tc.canon)
			}
		})
	}

	for _, source := range []string{"https://user:pass@example.com/catalog.json", "https://token@example.com/catalog.json"} {
		t.Run(source, func(t *testing.T) {
			_, err := ParseCatalogSource(source)
			if err == nil || !strings.Contains(err.Error(), "embedded credentials") {
				t.Fatalf("expected credential rejection, got %v", err)
			}
		})
	}
}

func TestParseCatalogSourceHTTPRequiresLoopbackHost(t *testing.T) {
	cases := []struct {
		name    string
		source  string
		wantErr bool
	}{
		{name: "ipv4 loopback", source: "http://127.0.0.1/catalog.json"},
		{name: "ipv6 loopback", source: "http://[::1]/catalog.json"},
		{name: "ipv4 mapped ipv6 loopback", source: "http://[::ffff:127.0.0.1]/catalog.json"},
		{name: "unspecified local bind", source: "http://0.0.0.0/catalog.json"},
		{name: "localhost suffix rejected", source: "http://localhost.example.com/catalog.json", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := ParseCatalogSource(tc.source)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseCatalogSource(%q) returned nil error", tc.source)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCatalogSource(%q): %v", tc.source, err)
			}
			if parsed.Kind != CatalogSourceHTTPS {
				t.Fatalf("kind = %s, want %s", parsed.Kind, CatalogSourceHTTPS)
			}
		})
	}
}

func testCatalogJSON() string {
	return `{
  "schemaVersion": 1,
  "id": "official",
  "owner": "Gitlawb",
  "description": "Official Zero plugins",
  "plugins": [
    {
      "id": "zero.demo",
      "name": "Zero Demo",
      "description": "Demo plugin",
      "author": {"name": "Zero"},
      "license": "MIT",
      "homepage": "https://example.com/zero.demo",
      "tags": ["demo", "docs"],
      "category": "productivity",
      "review": {
        "status": "reviewed",
        "date": "2026-07-10",
        "reviewer": "Zero Security",
        "url": "https://github.com/Gitlawb/zero-plugins/pull/1"
      },
      "releases": [
        {
          "version": "1.2.3",
          "repository": "https://github.com/Gitlawb/zero-demo-plugin.git",
          "commit": "` + strings.Repeat("a", 40) + `",
          "subdir": "plugins/demo",
          "treeHash": "sha256:` + strings.Repeat("b", 64) + `",
          "components": {
            "tools": [{"name": "lookup", "permission": "prompt"}],
            "hooks": [{"name": "preflight", "event": "beforeTool"}],
            "skills": [{"name": "review"}],
            "prompts": [{"name": "summarize"}]
          }
        },
        {
          "version": "1.2.4",
          "repository": "https://github.com/Gitlawb/zero-demo-plugin.git",
          "commit": "` + strings.Repeat("c", 40) + `",
          "treeHash": "sha256:` + strings.Repeat("d", 64) + `",
          "components": {"tools": [{"name": "lookup", "permission": "prompt"}]}
        }
      ]
    },
    {
      "id": "zero.second",
      "name": "Second",
      "author": {"name": "Zero"},
      "license": "MIT",
      "review": {
        "status": "community",
        "date": "2026-07-10",
        "reviewer": "Zero Security",
        "url": "https://github.com/Gitlawb/zero-plugins/pull/2"
      },
      "releases": [
        {
          "version": "0.1.0",
          "repository": "https://github.com/Gitlawb/zero-second-plugin.git",
          "commit": "` + strings.Repeat("e", 40) + `",
          "treeHash": "sha256:` + strings.Repeat("f", 64) + `",
          "components": {"prompts": [{"name": "review"}]}
        }
      ]
    }
  ]
}`
}
