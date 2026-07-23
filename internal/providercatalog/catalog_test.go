package providercatalog

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/kimiidentity"
)

var expectedCatalogIDs = []string{
	"gitlawb-opengateway",
	"aimlapi",
	"openai",
	"anthropic",
	"google",
	"ollama-cloud",
	"ollama",
	"lmstudio",
	"openrouter",
	"huggingface",
	"chatgpt",
	"kimi-code",
	"groq",
	"deepseek",
	"together",
	"dashscope",
	"moonshot",
	"atlascloud",
	"longcat",
	"nvidia-nim",
	"minimax",
	"minimaxi-cn",
	"mistral",
	"github",
	"bedrock",
	"vertex",
	"xai",
	"venice",
	"xiaomi-mimo",
	"bankr",
	"zai",
	"zai-cn",
	"kilocode",
	"opencode",
	"opencode-go",
	"opencode-go-anthropic-compatible",
	"atomic-chat",
	"chatgpt-proxy",
	"custom-openai-compatible",
	"custom-anthropic-compatible",
}

func TestAllHasStableUniqueIDs(t *testing.T) {
	descriptors := All()
	if len(descriptors) != len(expectedCatalogIDs) {
		t.Fatalf("All() returned %d descriptors, want %d", len(descriptors), len(expectedCatalogIDs))
	}

	seen := map[string]bool{}
	for index, descriptor := range descriptors {
		if descriptor.ID != expectedCatalogIDs[index] {
			t.Fatalf("All()[%d].ID = %q, want %q", index, descriptor.ID, expectedCatalogIDs[index])
		}
		if seen[descriptor.ID] {
			t.Fatalf("duplicate descriptor ID %q", descriptor.ID)
		}
		seen[descriptor.ID] = true
	}
	if !reflect.DeepEqual(IDs(), expectedCatalogIDs) {
		t.Fatalf("IDs() = %#v, want %#v", IDs(), expectedCatalogIDs)
	}
}

func TestRecommendedProvidersAreTopOfCatalog(t *testing.T) {
	descriptors := All()
	// The recommended providers are badged and pinned to the top of the catalog,
	// in this order (OpenGateway remains the default; aimlapi.com is also badged).
	wantTop := []string{"gitlawb-opengateway", "aimlapi"}
	if len(descriptors) < len(wantTop) {
		t.Fatalf("All() returned %d descriptors, want at least %d", len(descriptors), len(wantTop))
	}
	for index, id := range wantTop {
		if descriptors[index].ID != id {
			t.Fatalf("descriptors[%d] = %q, want %q", index, descriptors[index].ID, id)
		}
		if !descriptors[index].Recommended {
			t.Fatalf("descriptors[%d] (%q) should be recommended", index, id)
		}
	}
	// Exactly those are recommended, and they are contiguous at the top.
	recommended := 0
	for _, descriptor := range descriptors {
		if descriptor.Recommended {
			recommended++
		}
	}
	if recommended != len(wantTop) {
		t.Fatalf("recommended descriptor count = %d, want %d", recommended, len(wantTop))
	}
}

func TestRecommendedProviderEndpoint(t *testing.T) {
	descriptor, ok := Get("gitlawb-opengateway")
	if !ok {
		t.Fatal("gitlawb-opengateway not found in catalog")
	}
	if descriptor.DefaultBaseURL != "https://opengateway.gitlawb.com/v1" {
		t.Fatalf("OpenGateway base URL = %q, want %q", descriptor.DefaultBaseURL, "https://opengateway.gitlawb.com/v1")
	}
	if descriptor.Transport != TransportOpenAICompatible {
		t.Fatalf("OpenGateway transport = %q, want %q", descriptor.Transport, TransportOpenAICompatible)
	}
}

func TestAIMLAPIDescriptor(t *testing.T) {
	descriptor, err := Require("aimlapi")
	if err != nil {
		t.Fatalf("Require(aimlapi) error = %v", err)
	}
	if descriptor.Name != "aimlapi.com" {
		t.Fatalf("Name = %q, want aimlapi.com", descriptor.Name)
	}
	if descriptor.DefaultBaseURL != "https://api.aimlapi.com/v1" {
		t.Fatalf("DefaultBaseURL = %q, want aimlapi.com endpoint", descriptor.DefaultBaseURL)
	}
	if descriptor.DefaultModel != "anthropic/claude-sonnet-5" {
		t.Fatalf("DefaultModel = %q, want anthropic/claude-sonnet-5", descriptor.DefaultModel)
	}
	if descriptor.Transport != TransportOpenAICompatible {
		t.Fatalf("Transport = %q, want %q", descriptor.Transport, TransportOpenAICompatible)
	}
	if !reflect.DeepEqual(descriptor.AuthEnvVars, []string{"AIMLAPI_API_KEY"}) {
		t.Fatalf("AuthEnvVars = %#v, want AIMLAPI_API_KEY", descriptor.AuthEnvVars)
	}
}

func TestLongCatDescriptor(t *testing.T) {
	descriptor, err := Require("longcat")
	if err != nil {
		t.Fatalf("Require(longcat) error = %v", err)
	}
	if descriptor.Name != "LongCat" {
		t.Fatalf("Name = %q, want LongCat", descriptor.Name)
	}
	if descriptor.DefaultBaseURL != "https://api.longcat.chat/openai" {
		t.Fatalf("DefaultBaseURL = %q, want LongCat OpenAI-compatible endpoint", descriptor.DefaultBaseURL)
	}
	if descriptor.DefaultModel != "LongCat-2.0" {
		t.Fatalf("DefaultModel = %q, want LongCat-2.0", descriptor.DefaultModel)
	}
	if descriptor.Transport != TransportOpenAICompatible {
		t.Fatalf("Transport = %q, want %q", descriptor.Transport, TransportOpenAICompatible)
	}
	if !reflect.DeepEqual(descriptor.AuthEnvVars, []string{"LONGCAT_API_KEY"}) {
		t.Fatalf("AuthEnvVars = %#v, want LONGCAT_API_KEY", descriptor.AuthEnvVars)
	}
}

func TestAtlasCloudDescriptor(t *testing.T) {
	descriptor, err := Require("atlascloud")
	if err != nil {
		t.Fatalf("Require(atlascloud) error = %v", err)
	}
	if descriptor.Name != "Atlas Cloud" {
		t.Fatalf("Name = %q, want Atlas Cloud", descriptor.Name)
	}
	if descriptor.DefaultBaseURL != "https://api.atlascloud.ai/v1" {
		t.Fatalf("DefaultBaseURL = %q, want Atlas Cloud OpenAI-compatible endpoint", descriptor.DefaultBaseURL)
	}
	if descriptor.DefaultModel != "qwen/qwen3.5-flash" {
		t.Fatalf("DefaultModel = %q, want qwen/qwen3.5-flash", descriptor.DefaultModel)
	}
	if descriptor.Transport != TransportOpenAICompatible {
		t.Fatalf("Transport = %q, want %q", descriptor.Transport, TransportOpenAICompatible)
	}
	if !reflect.DeepEqual(descriptor.AuthEnvVars, []string{"ATLASCLOUD_API_KEY"}) {
		t.Fatalf("AuthEnvVars = %#v, want ATLASCLOUD_API_KEY", descriptor.AuthEnvVars)
	}
}

func TestCatalogDescriptorsExposeRequiredDefaults(t *testing.T) {
	for _, descriptor := range All() {
		if descriptor.ID == "" {
			t.Fatal("provider ID is required")
		}
		if descriptor.Name == "" {
			t.Fatalf("provider %q should expose a display name", descriptor.ID)
		}
		if descriptor.Transport == "" {
			t.Fatalf("provider %q should expose a transport", descriptor.ID)
		}
		if !ValidTransport(descriptor.Transport) {
			t.Fatalf("provider %q has unknown transport %q", descriptor.ID, descriptor.Transport)
		}
		if descriptor.DefaultBaseURL == "" {
			t.Fatalf("provider %q should expose a default base URL", descriptor.ID)
		}
		if descriptor.DefaultModel == "" {
			t.Fatalf("provider %q should expose a default model", descriptor.ID)
		}
		if len(descriptor.SupportedAPIFormats) == 0 {
			t.Fatalf("provider %q should expose at least one supported API format", descriptor.ID)
		}
		for _, format := range descriptor.SupportedAPIFormats {
			if !ValidAPIFormat(format) {
				t.Fatalf("provider %q has unknown API format %q", descriptor.ID, format)
			}
		}
	}
	if ValidTransport("missing") {
		t.Fatal("ValidTransport should reject unknown transports")
	}
	if ValidAPIFormat("missing") {
		t.Fatal("ValidAPIFormat should reject unknown API formats")
	}
}

func TestRemoteProvidersDeclareAuthOrExplicitPublicAccess(t *testing.T) {
	for _, descriptor := range All() {
		if descriptor.Local {
			continue
		}
		if descriptor.RequiresAuth && (len(descriptor.AuthEnvVars) > 0 || descriptor.UsesAmbientAuth) {
			continue
		}
		// OAuth-only providers (no API-key env var, no ambient auth) authenticate
		// via an interactive login flow rather than a credential env var. They
		// still require auth — the OAuthResolver populates the bearer at runtime.
		if descriptor.OAuth && descriptor.RequiresAuth {
			continue
		}
		if descriptor.Public && !descriptor.RequiresAuth {
			continue
		}
		t.Fatalf("%s is remote but declares neither credential env vars, ambient auth, nor public access", descriptor.ID)
	}
}

func TestLocalProvidersDoNotRequireAuth(t *testing.T) {
	for _, id := range []string{"ollama", "lmstudio"} {
		descriptor, err := Require(id)
		if err != nil {
			t.Fatalf("Require(%q) error = %v", id, err)
		}
		if !descriptor.Local {
			t.Fatalf("%s Local = false, want true", id)
		}
		if descriptor.RequiresAuth {
			t.Fatalf("%s RequiresAuth = true, want false", id)
		}
		if len(descriptor.AuthEnvVars) != 0 {
			t.Fatalf("%s AuthEnvVars = %#v, want empty", id, descriptor.AuthEnvVars)
		}
	}
}

func TestOllamaCloudAndLocalAreSeparateProviders(t *testing.T) {
	cloud, err := Require("ollama-cloud")
	if err != nil {
		t.Fatalf("Require(ollama-cloud) error = %v", err)
	}
	if cloud.Name != "Ollama Cloud" {
		t.Fatalf("ollama-cloud Name = %q, want Ollama Cloud", cloud.Name)
	}
	if cloud.DefaultBaseURL != "https://ollama.com/v1" {
		t.Fatalf("ollama-cloud DefaultBaseURL = %q, want https://ollama.com/v1", cloud.DefaultBaseURL)
	}
	if !cloud.RequiresAuth || cloud.Local {
		t.Fatalf("ollama-cloud auth/local flags = requiresAuth:%v local:%v, want remote auth provider", cloud.RequiresAuth, cloud.Local)
	}
	if len(cloud.AuthEnvVars) != 1 || cloud.AuthEnvVars[0] != "OLLAMA_API_KEY" {
		t.Fatalf("ollama-cloud AuthEnvVars = %#v, want OLLAMA_API_KEY", cloud.AuthEnvVars)
	}

	local, err := Require("ollama")
	if err != nil {
		t.Fatalf("Require(ollama) error = %v", err)
	}
	if local.Name != "Ollama Local" {
		t.Fatalf("ollama Name = %q, want Ollama Local", local.Name)
	}
	if local.DefaultBaseURL != "http://localhost:11434/v1" {
		t.Fatalf("ollama DefaultBaseURL = %q, want local OpenAI-compatible endpoint", local.DefaultBaseURL)
	}
	if !local.Local || local.RequiresAuth {
		t.Fatalf("ollama auth/local flags = requiresAuth:%v local:%v, want local no-auth provider", local.RequiresAuth, local.Local)
	}
}

func TestLookupNormalizesIDsAndAliases(t *testing.T) {
	cases := map[string]string{
		" OpenAI ":                     "openai",
		"Gemini":                       "google",
		"ollama cloud":                 "ollama-cloud",
		"ollama local":                 "ollama",
		"lm-studio":                    "lmstudio",
		"mini_max":                     "minimax",
		"Moonshot":                     "moonshot",
		"Atlas Cloud":                  "atlascloud",
		"nvidia nim":                   "nvidia-nim",
		"xiaomi mimo":                  "xiaomi-mimo",
		"custom_openai_compatible":     "custom-openai-compatible",
		"custom--anthropic compatible": "custom-anthropic-compatible",
		"GitLawb OpenGateway":          "gitlawb-opengateway",
	}
	for input, want := range cases {
		descriptor, ok := Get(input)
		if !ok {
			t.Fatalf("Get(%q) returned false", input)
		}
		if descriptor.ID != want {
			t.Fatalf("Get(%q).ID = %q, want %q", input, descriptor.ID, want)
		}

		required, err := Require(input)
		if err != nil {
			t.Fatalf("Require(%q) error = %v", input, err)
		}
		if required.ID != want {
			t.Fatalf("Require(%q).ID = %q, want %q", input, required.ID, want)
		}
	}

	if normalized := NormalizeID("custom--anthropic compatible"); normalized != "custom-anthropic-compatible" {
		t.Fatalf("NormalizeID() = %q, want custom-anthropic-compatible", normalized)
	}
	if _, ok := Get("unknown-provider"); ok {
		t.Fatal("Get should reject unknown provider IDs")
	}
	if _, err := Require("  Not_A_Provider  "); err == nil {
		t.Fatal("Require should reject unknown provider IDs")
	} else {
		if !errors.Is(err, ErrUnknownProvider) {
			t.Fatalf("Require error = %v, want ErrUnknownProvider", err)
		}
		if !strings.Contains(err.Error(), `unknown provider "not-a-provider"`) {
			t.Fatalf("Require error = %q, want normalized provider ID", err.Error())
		}
	}
}

func TestListByTransportPreservesCatalogOrder(t *testing.T) {
	cases := map[Transport][]string{
		TransportOpenAI:          {"openai"},
		TransportAnthropic:       {"anthropic"},
		TransportGoogle:          {"google"},
		TransportBedrock:         {"bedrock"},
		TransportVertex:          {"vertex"},
		TransportAnthropicCompat: {"minimax", "minimaxi-cn", "opencode-go-anthropic-compatible", "custom-anthropic-compatible"},
		TransportOpenAICompat:    {"gitlawb-opengateway", "aimlapi", "ollama-cloud", "ollama", "lmstudio", "openrouter", "huggingface", "chatgpt", "kimi-code", "groq", "deepseek", "together", "dashscope", "moonshot", "atlascloud", "longcat", "nvidia-nim", "mistral", "github", "xai", "venice", "xiaomi-mimo", "bankr", "zai", "zai-cn", "kilocode", "opencode", "opencode-go", "atomic-chat", "chatgpt-proxy", "custom-openai-compatible"},
	}

	for transport, wantIDs := range cases {
		descriptors := ListByTransport(transport)
		gotIDs := make([]string, 0, len(descriptors))
		for _, descriptor := range descriptors {
			if descriptor.Transport != Transport(NormalizeID(string(transport))) {
				t.Fatalf("ListByTransport(%q) returned provider %q with transport %q", transport, descriptor.ID, descriptor.Transport)
			}
			gotIDs = append(gotIDs, descriptor.ID)
		}
		if !reflect.DeepEqual(gotIDs, wantIDs) {
			t.Fatalf("ListByTransport(%q) IDs = %#v, want %#v", transport, gotIDs, wantIDs)
		}
	}
	if descriptors := ListByTransport("missing"); len(descriptors) != 0 {
		t.Fatalf("ListByTransport(missing) returned %#v, want empty", descriptors)
	}
	if gotIDs := descriptorIDs(ListByTransport(TransportOpenAICompatible)); !reflect.DeepEqual(gotIDs, cases[TransportOpenAICompat]) {
		t.Fatalf("ListByTransport(openai-compatible alias) IDs = %#v, want %#v", gotIDs, cases[TransportOpenAICompat])
	}
	if gotIDs := descriptorIDs(ListByTransport(TransportAnthropicCompatible)); !reflect.DeepEqual(gotIDs, cases[TransportAnthropicCompat]) {
		t.Fatalf("ListByTransport(anthropic-compatible alias) IDs = %#v, want %#v", gotIDs, cases[TransportAnthropicCompat])
	}
}

func TestReturnedDescriptorsAreCopies(t *testing.T) {
	descriptors := All()
	descriptors[0].ID = "changed"
	descriptors[0].AuthEnvVars[0] = "BROKEN"
	descriptors[0].SupportedAPIFormats[0] = "broken"

	descriptor, ok := Get("openai")
	if !ok {
		t.Fatal("Get(openai) returned false")
	}
	if descriptor.ID != "openai" {
		t.Fatalf("catalog entry mutated through All(): %#v", descriptor)
	}
	if descriptor.AuthEnvVars[0] != "OPENAI_API_KEY" {
		t.Fatalf("descriptor auth env vars are shared, got %q", descriptor.AuthEnvVars[0])
	}
	if descriptor.SupportedAPIFormats[0] != APIFormatOpenAIResponses {
		t.Fatalf("descriptor API formats are shared, got %q", descriptor.SupportedAPIFormats[0])
	}

	descriptor.AuthEnvVars[0] = "BROKEN-AGAIN"
	next, ok := Get("openai")
	if !ok {
		t.Fatal("Get(openai) returned false on second lookup")
	}
	if next.AuthEnvVars[0] != "OPENAI_API_KEY" {
		t.Fatalf("descriptor slices are shared, got %q", next.AuthEnvVars[0])
	}
}

func TestOAuthProviderClassification(t *testing.T) {
	oauthIDs := descriptorIDs(OAuthProviders())
	if want := []string{"openrouter", "huggingface", "chatgpt", "kimi-code", "xai"}; !reflect.DeepEqual(oauthIDs, want) {
		t.Fatalf("OAuthProviders() = %#v, want %#v", oauthIDs, want)
	}
	if d, _ := Get("openrouter"); !d.OAuthMintsKey {
		t.Fatal("openrouter should mint a key")
	}
	if d, _ := Get("xai"); !d.OAuthDeviceFlow {
		t.Fatal("xai should advertise device-code flow")
	}
	if d, _ := Get("kimi-code"); !d.OAuthDeviceFlow || !d.OAuthDeviceOnly {
		t.Fatal("kimi-code should advertise device-only code flow")
	}
	if d, _ := Get("huggingface"); !d.OAuthDeviceFlow {
		t.Fatal("huggingface should advertise device-code flow")
	}
	if d, _ := Get("chatgpt"); d.OAuthDeviceFlow {
		t.Fatal("chatgpt should NOT advertise device-code flow (loopback only)")
	}
}

// TestKimiAliasStillResolvesToMoonshot pins the alias-collision fix: moonshot
// already exposes "kimi" as an alias for its API-key endpoint. The Kimi Code
// OAuth descriptor must use a non-conflicting ID ("kimi-code") so resolving
// "kimi" continues to land on moonshot (endpoint, default model, MOONSHOT_API_KEY).
func TestKimiAliasStillResolvesToMoonshot(t *testing.T) {
	d, ok := Get("kimi")
	if !ok {
		t.Fatal(`Get("kimi") returned false`)
	}
	if d.ID != "moonshot" {
		t.Fatalf(`Get("kimi").ID = %q, want "moonshot" (kimi-code must not steal this alias)`, d.ID)
	}
	if d.DefaultBaseURL != "https://api.moonshot.ai/v1" {
		t.Fatalf(`Get("kimi").DefaultBaseURL = %q, want moonshot API-key endpoint`, d.DefaultBaseURL)
	}
	if len(d.AuthEnvVars) == 0 || d.AuthEnvVars[0] != "MOONSHOT_API_KEY" {
		t.Fatalf(`Get("kimi").AuthEnvVars = %#v, want MOONSHOT_API_KEY`, d.AuthEnvVars)
	}
	if d.OAuth {
		t.Fatal(`Get("kimi") must not be OAuth (that is kimi-code, not the moonshot alias)`)
	}

	code, ok := Get("kimi-code")
	if !ok {
		t.Fatal(`Get("kimi-code") returned false`)
	}
	if code.ID != "kimi-code" {
		t.Fatalf(`Get("kimi-code").ID = %q`, code.ID)
	}
	if code.DefaultBaseURL != "https://api.kimi.com/coding/v1" {
		t.Fatalf(`Get("kimi-code").DefaultBaseURL = %q, want managed coding endpoint`, code.DefaultBaseURL)
	}
	if !code.OAuth || !code.OAuthDeviceOnly {
		t.Fatalf(`Get("kimi-code") oauth flags wrong: OAuth=%v OAuthDeviceOnly=%v`, code.OAuth, code.OAuthDeviceOnly)
	}
}

// TestKimiRuntimeHeadersOnlyOnGet ensures listing providers (All / OAuthProviders)
// does not populate kimi-code's CustomHeaders (which mints a device-id file),
// while Get does so resolve-time request building still gets the vendor headers.
func TestKimiRuntimeHeadersOnlyOnGet(t *testing.T) {
	tempDir := t.TempDir()
	cleanup := kimiidentity.SetDeviceIDPathForTest(filepath.Join(tempDir, "kimi-device-id"))
	t.Cleanup(cleanup)
	t.Setenv("XDG_CONFIG_HOME", tempDir)
	t.Setenv("APPDATA", tempDir)
	for _, d := range All() {
		if d.ID == "kimi-code" && d.CustomHeaders != nil {
			t.Fatalf("All() must not populate kimi-code CustomHeaders: %#v", d.CustomHeaders)
		}
	}
	for _, d := range OAuthProviders() {
		if d.ID == "kimi-code" && d.CustomHeaders != nil {
			t.Fatalf("OAuthProviders() must not populate kimi-code CustomHeaders: %#v", d.CustomHeaders)
		}
	}
	d, ok := Get("kimi-code")
	if !ok {
		t.Fatal(`Get("kimi-code") returned false`)
	}
	for _, header := range []string{"X-Msh-Platform", "X-Msh-Version", "X-Msh-Device-Name", "X-Msh-Device-Model", "X-Msh-Os-Version", "X-Msh-Device-Id"} {
		if d.CustomHeaders[header] == "" {
			t.Fatalf("Get(kimi-code).CustomHeaders[%q] empty, want vendor-identity header for completions", header)
		}
	}
}

func descriptorIDs(descriptors []Descriptor) []string {
	ids := make([]string, 0, len(descriptors))
	for _, descriptor := range descriptors {
		ids = append(ids, descriptor.ID)
	}
	return ids
}
