package kafkacptschedule

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
	return loc
}

func TestComputeNextCutoffs_SingleDailyCutoff(t *testing.T) {
	sched := scheduleData{
		SiteId:   "site-1",
		Timezone: "UTC",
		Cutoffs: []cutoffData{
			{CptId: "sp1-1800", LocalTime: "18:00", EligiblePathIds: []string{"pick", "singles"}},
		},
	}
	from := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC) // a Sunday
	got, err := computeNextCutoffs(sched, from, 3)
	if err != nil {
		t.Fatalf("computeNextCutoffs: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d windows, want 3", len(got))
	}
	want0 := time.Date(2026, 9, 13, 18, 0, 0, 0, time.UTC)
	if !got[0].CutoffAt.Equal(want0) {
		t.Fatalf("first cutoff = %v, want %v", got[0].CutoffAt, want0)
	}
	if got[0].CptId != "sp1-1800" {
		t.Fatalf("CptId = %q, want sp1-1800", got[0].CptId)
	}
	if len(got[0].EligiblePathIds) != 2 {
		t.Fatalf("EligiblePathIds = %v", got[0].EligiblePathIds)
	}
	want1 := want0.AddDate(0, 0, 1)
	if !got[1].CutoffAt.Equal(want1) {
		t.Fatalf("second cutoff = %v, want %v", got[1].CutoffAt, want1)
	}
}

func TestComputeNextCutoffs_AfterTodaysCutoffSkipsToTomorrow(t *testing.T) {
	sched := scheduleData{
		SiteId:   "site-1",
		Timezone: "UTC",
		Cutoffs: []cutoffData{
			{CptId: "sp1-1800", LocalTime: "18:00"},
		},
	}
	// from is already past today's 18:00 cutoff.
	from := time.Date(2026, 9, 13, 19, 0, 0, 0, time.UTC)
	got, err := computeNextCutoffs(sched, from, 1)
	if err != nil {
		t.Fatalf("computeNextCutoffs: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d windows, want 1", len(got))
	}
	want := time.Date(2026, 9, 14, 18, 0, 0, 0, time.UTC)
	if !got[0].CutoffAt.Equal(want) {
		t.Fatalf("cutoff = %v, want %v", got[0].CutoffAt, want)
	}
}

func TestComputeNextCutoffs_DaysOfWeekFilter(t *testing.T) {
	sched := scheduleData{
		SiteId:   "site-1",
		Timezone: "UTC",
		Cutoffs: []cutoffData{
			// Only Mon/Wed/Fri.
			{CptId: "mwf-1800", LocalTime: "18:00", DaysOfWeek: []string{"Mon", "Wed", "Fri"}},
		},
	}
	// 2026-09-13 is a Sunday.
	from := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	got, err := computeNextCutoffs(sched, from, 1)
	if err != nil {
		t.Fatalf("computeNextCutoffs: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d windows, want 1", len(got))
	}
	// The next Monday is 2026-09-14.
	want := time.Date(2026, 9, 14, 18, 0, 0, 0, time.UTC)
	if !got[0].CutoffAt.Equal(want) {
		t.Fatalf("cutoff = %v, want %v", got[0].CutoffAt, want)
	}
}

// TestComputeNextCutoffs_TimezoneCrossingMidnight is the required
// timezone-crossing-midnight case: a schedule in a timezone west of UTC
// with a late local cutoff crosses into the next UTC calendar day, and
// the computed instant must still be correct in absolute (UTC) terms.
func TestComputeNextCutoffs_TimezoneCrossingMidnight(t *testing.T) {
	loc := mustLoc(t, "America/Los_Angeles")
	sched := scheduleData{
		SiteId:   "site-la",
		Timezone: "America/Los_Angeles",
		Cutoffs: []cutoffData{
			// 23:30 local time -- crosses into the next UTC day.
			{CptId: "la-2330", LocalTime: "23:30"},
		},
	}
	// from is 2026-09-13 08:00 local (well before the 23:30 cutoff).
	from := time.Date(2026, 9, 13, 8, 0, 0, 0, loc)
	got, err := computeNextCutoffs(sched, from, 1)
	if err != nil {
		t.Fatalf("computeNextCutoffs: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d windows, want 1", len(got))
	}
	wantLocal := time.Date(2026, 9, 13, 23, 30, 0, 0, loc)
	if !got[0].CutoffAt.Equal(wantLocal) {
		t.Fatalf("cutoff = %v, want %v", got[0].CutoffAt, wantLocal)
	}
	// In UTC (PDT is UTC-7), this is 2026-09-14 06:30 UTC — a later
	// calendar day. Assert on the UTC representation explicitly so a
	// naive implementation that computed only in UTC-of-the-same-day
	// would fail this check.
	wantUTC := time.Date(2026, 9, 14, 6, 30, 0, 0, time.UTC)
	if !got[0].CutoffAt.UTC().Equal(wantUTC) {
		t.Fatalf("cutoff in UTC = %v, want %v", got[0].CutoffAt.UTC(), wantUTC)
	}
}

// TestComputeNextCutoffs_NoCutoffInHorizon is the required "no cutoff in
// the next N days" edge case: a days-of-week filter that matches no day
// within the search horizon must return an empty, non-error result.
func TestComputeNextCutoffs_NoCutoffInHorizon(t *testing.T) {
	sched := scheduleData{
		SiteId:   "site-1",
		Timezone: "UTC",
		Cutoffs: []cutoffData{
			// A day-of-week value that will never match any real day.
			{CptId: "never", LocalTime: "12:00", DaysOfWeek: []string{"Nonexistent"}},
		},
	}
	from := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	got, err := computeNextCutoffs(sched, from, 5)
	if err != nil {
		t.Fatalf("computeNextCutoffs: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d windows, want 0", len(got))
	}
}

func TestComputeNextCutoffs_MultipleCutoffsMergedAndSorted(t *testing.T) {
	sched := scheduleData{
		SiteId:   "site-1",
		Timezone: "UTC",
		Cutoffs: []cutoffData{
			{CptId: "evening", LocalTime: "18:00"},
			{CptId: "morning", LocalTime: "08:00"},
		},
	}
	from := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	got, err := computeNextCutoffs(sched, from, 2)
	if err != nil {
		t.Fatalf("computeNextCutoffs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d windows, want 2", len(got))
	}
	if got[0].CptId != "morning" {
		t.Fatalf("first cutoff = %q, want morning (earlier in the day)", got[0].CptId)
	}
	if got[1].CptId != "evening" {
		t.Fatalf("second cutoff = %q, want evening", got[1].CptId)
	}
}

func TestComputeNextCutoffs_InvalidTimezone(t *testing.T) {
	sched := scheduleData{SiteId: "site-1", Timezone: "Not/AZone", Cutoffs: []cutoffData{{CptId: "x", LocalTime: "12:00"}}}
	if _, err := computeNextCutoffs(sched, time.Now(), 1); err == nil {
		t.Fatal("expected an error for an invalid timezone")
	}
}

func TestComputeNextCutoffs_InvalidLocalTime(t *testing.T) {
	sched := scheduleData{SiteId: "site-1", Timezone: "UTC", Cutoffs: []cutoffData{{CptId: "x", LocalTime: "25:99"}}}
	if _, err := computeNextCutoffs(sched, time.Now(), 1); err == nil {
		t.Fatal("expected an error for an invalid local_time")
	}
}

func TestComputeNextCutoffs_ZeroNReturnsNothing(t *testing.T) {
	sched := scheduleData{SiteId: "site-1", Timezone: "UTC", Cutoffs: []cutoffData{{CptId: "x", LocalTime: "12:00"}}}
	got, err := computeNextCutoffs(sched, time.Now(), 0)
	if err != nil {
		t.Fatalf("computeNextCutoffs: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d windows, want 0", len(got))
	}
}
