package config

import "testing"

func TestParseWebhookFilesMaxMB(t *testing.T) {
	const mb = 1024 * 1024
	cases := map[string]int64{
		"":     DefaultWebhookFilesMaxMB * mb,
		"  ":   DefaultWebhookFilesMaxMB * mb,
		"abc":  DefaultWebhookFilesMaxMB * mb,
		"10":   10 * mb,
		" 64 ": 64 * mb,
		"0":    0,
		"-1":   0,
	}
	for raw, want := range cases {
		if got := ParseWebhookFilesMaxMB(raw); got != want {
			t.Errorf("ParseWebhookFilesMaxMB(%q) = %d, want %d", raw, got, want)
		}
	}
}
