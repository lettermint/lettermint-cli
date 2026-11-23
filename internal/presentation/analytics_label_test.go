package presentation

import "testing"

func TestHourlyBucketLabelsKeepDaylightSavingOffsets(t *testing.T) {
	first := bucketLabel("2026-10-25T02:00:00+02:00", "hour")
	second := bucketLabel("2026-10-25T02:00:00+01:00", "hour")
	if first == second || first != "2026-10-25 02:00 +02:00" || second != "2026-10-25 02:00 +01:00" {
		t.Fatalf("first=%q second=%q", first, second)
	}
	if day := bucketLabel("2026-10-25T00:00:00+02:00", "day"); day != "2026-10-25" {
		t.Fatalf("day=%q", day)
	}
}
