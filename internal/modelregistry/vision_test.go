package modelregistry

import "testing"

func TestSupportsVision(t *testing.T) {
	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry returned error: %v", err)
	}

	// A registry containing exactly one non-vision model so the false case is
	// covered even though every entry in the default catalog has vision.
	nonVision := validModelEntry()
	nonVision.ID = "no-vision-model"
	nonVision.APIModel = "no-vision-model"
	nonVision.Aliases = []string{"openai:no-vision-model"}
	nonVision.Capabilities = ModelCapabilities{ModelCapabilityChat}
	if nonVision.Supports(ModelCapabilityVision) {
		t.Fatal("test fixture should not advertise vision")
	}
	nonVisionRegistry, err := NewRegistry([]ModelEntry{nonVision})
	if err != nil {
		t.Fatalf("NewRegistry returned error: %v", err)
	}

	cases := []struct {
		name     string
		registry Registry
		modelID  string
		want     bool
	}{
		{name: "vision model is supported", registry: registry, modelID: "gemini-2.5-pro", want: true},
		{name: "vision model resolves via alias", registry: registry, modelID: "gemini-pro", want: true},
		{name: "non-vision model is not supported", registry: nonVisionRegistry, modelID: "no-vision-model", want: false},
		{name: "unknown model is not supported", registry: registry, modelID: "totally-made-up-model", want: false},
		{name: "empty model id is not supported", registry: registry, modelID: "", want: false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := SupportsVision(testCase.registry, testCase.modelID); got != testCase.want {
				t.Fatalf("SupportsVision(%q) = %v, want %v", testCase.modelID, got, testCase.want)
			}
		})
	}
}
