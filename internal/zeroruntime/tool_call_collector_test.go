package zeroruntime

import "testing"

func TestToolCallCollectorPreservesProviderMetadataAndFreeformState(t *testing.T) {
	collector := newToolCallCollector()
	collector.start("public-1", "provider-1", "apply_patch", "sig-1", true)
	collector.delta("public-1", "raw patch")
	collected := &CollectedStream{}
	collector.flush(collected)

	if len(collected.ToolCalls) != 1 {
		t.Fatalf("tool calls = %#v, want one", collected.ToolCalls)
	}
	call := collected.ToolCalls[0]
	if call.ID != "public-1" || call.ProviderCallID != "provider-1" || call.Name != "apply_patch" ||
		call.Signature != "sig-1" || call.Arguments != "raw patch" || !call.Freeform {
		t.Fatalf("collected tool call lost provider metadata: %#v", call)
	}
}

func TestToolCallCollectorPreservesEmptyPublicID(t *testing.T) {
	collector := newToolCallCollector()
	collector.start("", "provider-1", "read_file", "", false)
	collector.delta("", `{"path":"README.md"}`)
	collector.end("")
	collected := &CollectedStream{}
	collector.flush(collected)

	if len(collected.ToolCalls) != 1 {
		t.Fatalf("tool calls = %#v, want one", collected.ToolCalls)
	}
	call := collected.ToolCalls[0]
	if call.ID != "" || call.ProviderCallID != "provider-1" || call.Name != "read_file" {
		t.Fatalf("empty-public-id tool call = %#v", call)
	}
}
