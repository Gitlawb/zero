package providercatalog

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

var expectedCatalogIDs = []string{
	"openai",
	"anthropic",
	"google",
	"ollama",
	"lmstudio",
	"openrouter",
	"groq",
	"deepseek",
	"together",
	"dashscope",
	"moonshot",
	"nvidia-nim",
	"minimax",
	"mistral",
	"github",
	"bedrock",
	"vertex",
	"xai",
	"venice",
	"xiaomi-mimo",
	"bankr",
	"zai",
	"gitlawb-opengateway",
	"atomic-chat",
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

func TestLookupNormalizesIDsAndAliases(t *testing.T) {
	cases := map[string]string{
		" OpenAI ":                     "openai",
		"Gemini":                       "google",
		"lm-studio":                    "lmstudio",
		"mini_max":                     "minimax",
		"Moonshot":                     "moonshot",
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
		TransportAnthropicCompat: {"minimax", "custom-anthropic-compatible"},
		TransportOpenAICompat:    {"ollama", "lmstudio", "openrouter", "groq", "deepseek", "together", "dashscope", "moonshot", "nvidia-nim", "mistral", "github", "xai", "venice", "xiaomi-mimo", "bankr", "zai", "gitlawb-opengateway", "atomic-chat", "custom-openai-compatible"},
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

func descriptorIDs(descriptors []Descriptor) []string {
	ids := make([]string, 0, len(descriptors))
	for _, descriptor := range descriptors {
		ids = append(ids, descriptor.ID)
	}
	return ids
}
