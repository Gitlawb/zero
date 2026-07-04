package usage

import "testing"

func TestSummaryCacheHitRate(t *testing.T) {
	s := Summary{InputTokens: 13100, CachedInputTokens: 8200}
	if got := s.CacheHitRate(); got < 0.62 || got > 0.63 {
		t.Fatalf("CacheHitRate = %v, want ~0.626", got)
	}
	if got := (Summary{}).CacheHitRate(); got != 0 {
		t.Fatalf("empty CacheHitRate = %v, want 0", got)
	}
}

func TestFormatCacheEfficiency(t *testing.T) {
	s := Summary{InputTokens: 13100, CachedInputTokens: 8200}
	if got := FormatCacheEfficiency(s); got != "63% (8,200 cached / 13,100 input)" {
		t.Fatalf("FormatCacheEfficiency = %q", got)
	}
	if got := FormatCacheEfficiency(Summary{}); got != "n/a" {
		t.Fatalf("empty FormatCacheEfficiency = %q, want n/a", got)
	}
}
