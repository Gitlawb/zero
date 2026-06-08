package usage

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/Gitlawb/zero/internal/modelregistry"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// usageEventPayload mirrors the persisted EventUsage payload written by the exec
// runtime. Only token counts are stored — model id, cost, and the cached /
// reasoning split are not persisted — so report cost is reconstructed from each
// session's Metadata.ModelID and is therefore a labeled estimate.
type usageEventPayload struct {
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`
	TotalTokens      int `json:"totalTokens"`
}

// DayBucket aggregates usage events sharing the same UTC calendar date.
type DayBucket struct {
	Date         string  `json:"date"`
	Requests     int     `json:"requests"`
	InputTokens  int     `json:"inputTokens"`
	OutputTokens int     `json:"outputTokens"`
	TotalTokens  int     `json:"totalTokens"`
	TotalCost    float64 `json:"totalCost"`
}

// Totals carries report-wide sums across every bucket.
type Totals struct {
	Requests     int     `json:"requests"`
	InputTokens  int     `json:"inputTokens"`
	OutputTokens int     `json:"outputTokens"`
	TotalTokens  int     `json:"totalTokens"`
	TotalCost    float64 `json:"totalCost"`
}

// Report is the aggregated usage view rendered by `zero usage report`. Cost is a
// reconstructed estimate (see usageEventPayload) and NetLOC is a working-tree
// estimate; both are surfaced as estimates in the rendered output.
type Report struct {
	Buckets         []DayBucket `json:"buckets"`
	Total           Totals      `json:"total"`
	NetLOC          int         `json:"netLOC"`
	NetLOCPositive  bool        `json:"netLOCPositive"`
	TokensPerNetLOC float64     `json:"tokensPerNetLOC"`
	CostPerNetLOC   float64     `json:"costPerNetLOC"`
	LOCEstimated    bool        `json:"locEstimated"`
	CostEstimated   bool        `json:"costEstimated"`
}

// BuildReport aggregates persisted EventUsage events into per-day buckets and a
// report-wide total, reconstructing cost from the owning session's
// Metadata.ModelID via modelregistry.CalculateCost. Sessions whose model id is
// empty or unknown contribute token counts but no cost. The per-net-LOC ratios
// are guarded against a non-positive netLOC.
func BuildReport(events []sessions.Event, meta []sessions.Metadata, registry *modelregistry.Registry, netLOC int) (Report, error) {
	modelBySession := map[string]string{}
	for _, m := range meta {
		modelBySession[m.SessionID] = m.ModelID
	}

	buckets := map[string]*DayBucket{}
	report := Report{
		NetLOC:        netLOC,
		LOCEstimated:  true,
		CostEstimated: true,
	}

	for _, event := range events {
		if event.Type != sessions.EventUsage {
			continue
		}
		var payload usageEventPayload
		if len(event.Payload) > 0 {
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return Report{}, err
			}
		}

		date := utcDayBucket(event.CreatedAt)
		bucket, ok := buckets[date]
		if !ok {
			bucket = &DayBucket{Date: date}
			buckets[date] = bucket
		}

		bucket.Requests++
		bucket.InputTokens += payload.PromptTokens
		bucket.OutputTokens += payload.CompletionTokens
		bucket.TotalTokens += payload.TotalTokens

		report.Total.Requests++
		report.Total.InputTokens += payload.PromptTokens
		report.Total.OutputTokens += payload.CompletionTokens
		report.Total.TotalTokens += payload.TotalTokens

		modelID := modelBySession[event.SessionID]
		if modelID == "" || registry == nil {
			continue
		}
		model, err := registry.Require(modelID)
		if err != nil {
			continue
		}
		cost, err := modelregistry.CalculateCost(model, zeroruntime.Usage{
			InputTokens:  payload.PromptTokens,
			OutputTokens: payload.CompletionTokens,
		})
		if err != nil {
			continue
		}
		bucket.TotalCost += cost.TotalCost
		report.Total.TotalCost += cost.TotalCost
	}

	report.Buckets = make([]DayBucket, 0, len(buckets))
	for _, bucket := range buckets {
		report.Buckets = append(report.Buckets, *bucket)
	}
	sort.SliceStable(report.Buckets, func(left int, right int) bool {
		return report.Buckets[left].Date < report.Buckets[right].Date
	})

	if netLOC > 0 {
		report.NetLOCPositive = true
		report.TokensPerNetLOC = float64(report.Total.TotalTokens) / float64(netLOC)
		report.CostPerNetLOC = report.Total.TotalCost / float64(netLOC)
	}
	return report, nil
}

// utcDayBucket maps an RFC3339 timestamp to its UTC calendar date (YYYY-MM-DD).
// Normalizing to UTC first keeps an offset timestamp (e.g. ...T23:30:00-07:00,
// which is the next UTC day) bucketed by its true UTC day. On a parse failure it
// falls back to the leading-10 slice so malformed timestamps still bucket
// defensively rather than collapsing into one empty-string bucket.
func utcDayBucket(createdAt string) string {
	if parsed, err := time.Parse(time.RFC3339, createdAt); err == nil {
		return parsed.UTC().Format("2006-01-02")
	}
	if len(createdAt) >= 10 {
		return createdAt[:10]
	}
	return createdAt
}
