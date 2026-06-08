package usage

import (
	"encoding/json"
	"testing"

	"github.com/Gitlawb/zero/internal/modelregistry"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

func usageEvent(t *testing.T, sessionID string, sequence int, createdAt string, prompt int, completion int) sessions.Event {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"promptTokens":     prompt,
		"completionTokens": completion,
		"totalTokens":      prompt + completion,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return sessions.Event{
		SessionID: sessionID,
		Sequence:  sequence,
		Type:      sessions.EventUsage,
		CreatedAt: createdAt,
		Payload:   payload,
	}
}

func TestBuildReportBucketsByDayAndSumsTokens(t *testing.T) {
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	events := []sessions.Event{
		usageEvent(t, "s1", 1, "2026-06-01T09:00:00Z", 1000, 200),
		usageEvent(t, "s1", 2, "2026-06-01T11:00:00Z", 500, 100),
		usageEvent(t, "s2", 1, "2026-06-02T09:00:00Z", 2000, 400),
	}
	meta := []sessions.Metadata{
		{SessionID: "s1", ModelID: "gpt-4.1"},
		{SessionID: "s2", ModelID: "gpt-4.1"},
	}

	report, err := BuildReport(events, meta, &registry, 70)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if len(report.Buckets) != 2 {
		t.Fatalf("expected 2 day buckets, got %d", len(report.Buckets))
	}
	if report.Buckets[0].Date != "2026-06-01" || report.Buckets[1].Date != "2026-06-02" {
		t.Fatalf("buckets not sorted by date: %+v", report.Buckets)
	}
	if report.Buckets[0].Requests != 2 || report.Buckets[0].TotalTokens != 1800 {
		t.Fatalf("day-1 aggregation wrong: %+v", report.Buckets[0])
	}
	if report.Total.Requests != 3 || report.Total.TotalTokens != 4200 {
		t.Fatalf("totals wrong: %+v", report.Total)
	}
	if !report.LOCEstimated {
		t.Fatalf("expected LOCEstimated=true")
	}
	if report.NetLOC != 70 {
		t.Fatalf("NetLOC = %d, want 70", report.NetLOC)
	}
}

func TestBuildReportReconstructsCostFromMetadataModel(t *testing.T) {
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	events := []sessions.Event{
		usageEvent(t, "s1", 1, "2026-06-01T09:00:00Z", 1000, 200),
	}
	meta := []sessions.Metadata{{SessionID: "s1", ModelID: "gpt-4.1"}}

	report, err := BuildReport(events, meta, &registry, 10)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}

	model, err := registry.Require("gpt-4.1")
	if err != nil {
		t.Fatalf("Require: %v", err)
	}
	want, err := modelregistry.CalculateCost(model, zeroruntime.Usage{InputTokens: 1000, OutputTokens: 200})
	if err != nil {
		t.Fatalf("CalculateCost: %v", err)
	}
	if report.Total.TotalCost != want.TotalCost {
		t.Fatalf("reconstructed cost = %v, want %v", report.Total.TotalCost, want.TotalCost)
	}
}

func TestBuildReportRatiosGuardNetZero(t *testing.T) {
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	events := []sessions.Event{usageEvent(t, "s1", 1, "2026-06-01T09:00:00Z", 1000, 200)}
	meta := []sessions.Metadata{{SessionID: "s1", ModelID: "gpt-4.1"}}

	report, err := BuildReport(events, meta, &registry, 0)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if report.TokensPerNetLOC != 0 || report.CostPerNetLOC != 0 {
		t.Fatalf("expected zeroed ratios for net<=0, got tokens=%v cost=%v", report.TokensPerNetLOC, report.CostPerNetLOC)
	}
	if report.NetLOCPositive {
		t.Fatalf("expected NetLOCPositive=false for net=0")
	}

	report, err = BuildReport(events, meta, &registry, 600)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if !report.NetLOCPositive || report.TokensPerNetLOC != 2 {
		t.Fatalf("expected tokens/net = 1200/600 = 2, got %v (positive=%v)", report.TokensPerNetLOC, report.NetLOCPositive)
	}
}

func TestBuildReportIgnoresNonUsageEvents(t *testing.T) {
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	events := []sessions.Event{
		{SessionID: "s1", Sequence: 1, Type: sessions.EventMessage, CreatedAt: "2026-06-01T09:00:00Z"},
		usageEvent(t, "s1", 2, "2026-06-01T09:30:00Z", 1000, 200),
	}
	meta := []sessions.Metadata{{SessionID: "s1", ModelID: "gpt-4.1"}}

	report, err := BuildReport(events, meta, &registry, 10)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if report.Total.Requests != 1 {
		t.Fatalf("expected non-usage event ignored, got %d requests", report.Total.Requests)
	}
}
