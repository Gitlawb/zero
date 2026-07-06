package config

import (
	"strings"
	"testing"
)

func TestValidateSTTConfigRejectsUnknownProvider(t *testing.T) {
	_, issues := ValidateBytes([]byte(`{"stt":{"provider":"assemblyai"}}`))
	if len(issues) == 0 {
		t.Fatal("expected an issue for unknown stt.provider")
	}
	if !strings.Contains(issues[0].Message, "assemblyai") || !strings.Contains(issues[0].Message, "local, groq, or openai") {
		t.Errorf("issue message = %q, want it to name the bad value and valid options", issues[0].Message)
	}
}

func TestValidateSTTConfigRejectsUnknownStreamProvider(t *testing.T) {
	_, issues := ValidateBytes([]byte(`{"stt":{"streamProvider":"groq"}}`))
	if len(issues) == 0 {
		t.Fatal("expected an issue: groq has no streaming product, not a valid streamProvider")
	}
	if !strings.Contains(issues[0].Message, "deepgram") {
		t.Errorf("issue message = %q", issues[0].Message)
	}
}

func TestValidateSTTConfigAcceptsValid(t *testing.T) {
	_, issues := ValidateBytes([]byte(`{"stt":{"provider":"groq","streamProvider":"deepgram","maxDurationSeconds":120}}`))
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %+v", issues)
	}
}

func TestValidateSTTConfigRejectsNegativeDuration(t *testing.T) {
	_, issues := ValidateBytes([]byte(`{"stt":{"maxDurationSeconds":-5}}`))
	if len(issues) == 0 {
		t.Fatal("expected an issue for negative maxDurationSeconds")
	}
}

func TestSTTConfigDefaults(t *testing.T) {
	cfg := STTConfig{}
	if cfg.STTProvider() != STTProviderLocal {
		t.Errorf("default provider = %q, want local", cfg.STTProvider())
	}
	if cfg.STTStreamProvider() != STTProviderLocal {
		t.Errorf("default streamProvider = %q, want local", cfg.STTStreamProvider())
	}
	if !cfg.StreamingEnabled() {
		t.Error("streaming should default on")
	}
	if !cfg.SilenceAutoStopEnabled() {
		t.Error("silence auto-stop should default on")
	}
	if cfg.AutoSubmitEnabled() {
		t.Error("auto-submit should default off (insert-for-review is the safety net)")
	}
	if !cfg.Empty() {
		t.Error("zero-value STTConfig should be Empty")
	}
}

func TestSTTConfigTriStateBools(t *testing.T) {
	cfg, issues := ValidateBytes([]byte(`{"stt":{"autoSubmit":true,"streaming":false,"silenceAutoStop":false}}`))
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %+v", issues)
	}
	if !cfg.STT.AutoSubmitEnabled() {
		t.Error("autoSubmit:true should be enabled")
	}
	if cfg.STT.StreamingEnabled() {
		t.Error("streaming:false should be disabled")
	}
	if cfg.STT.SilenceAutoStopEnabled() {
		t.Error("silenceAutoStop:false should be disabled")
	}
}

func TestSTTConfigRoundTripsThroughMarshal(t *testing.T) {
	cfg, _ := ValidateBytes([]byte(`{"stt":{"provider":"groq","model":"whisper-large-v3-turbo","localServerPort":6007}}`))
	data, err := cfg.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"stt"`) {
		t.Errorf("marshaled config dropped stt section: %s", data)
	}
	reparsed, issues := ValidateBytes(data)
	if len(issues) != 0 {
		t.Fatalf("re-parse issues: %+v", issues)
	}
	if reparsed.STT.Provider != STTProviderGroq || reparsed.STT.Model != "whisper-large-v3-turbo" || reparsed.STT.LocalServerPort != 6007 {
		t.Errorf("round-trip lost fields: %+v", reparsed.STT)
	}
}

func TestSetSTTModelPersists(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.json"
	// Cloud provider stores under stt.model.
	if _, err := SetSTTModel(path, STTProviderGroq, "whisper-large-v3-turbo"); err != nil {
		t.Fatalf("SetSTTModel: %v", err)
	}
	cfg, issues := ValidateFile(path)
	if len(issues) != 0 {
		t.Fatalf("issues: %+v", issues)
	}
	if cfg.STT.Provider != STTProviderGroq || cfg.STT.Model != "whisper-large-v3-turbo" {
		t.Errorf("persisted STT = %+v", cfg.STT)
	}
	// Local provider stores under stt.localModelPath.
	if _, err := SetSTTModel(path, STTProviderLocal, "/models/moonshine"); err != nil {
		t.Fatalf("SetSTTModel local: %v", err)
	}
	cfg, _ = ValidateFile(path)
	if cfg.STT.LocalModelPath != "/models/moonshine" {
		t.Errorf("local model path = %q", cfg.STT.LocalModelPath)
	}
}
