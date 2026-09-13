package kafkacptschedule

import (
	"fmt"
	"sort"
	"time"

	"github.com/claudioed/order-management/internal/domain/order"
)

// weekdayAbbrev maps Go's time.Weekday to the wire's "Mon".."Sun"
// convention (process-path-management's shared.Weekday constants,
// mirrored verbatim as plain strings here since this context never
// imports that service's Go packages).
var weekdayAbbrev = [...]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

// computeNextCutoffs walks forward from `from`, day by day, up to
// cptSearchHorizonDays, evaluating every cutoff in sched against each
// day's date in sched's own IANA timezone, and returns the n earliest
// concrete occurrences (strictly after `from`) across all cutoffs,
// sorted ascending by CutoffAt.
//
// Returns an error only for a malformed schedule (bad timezone or an
// unparseable local_time on some cutoff) — the caller (Consumer.NextCutoffs)
// turns that into known=false, matching PromisePolicy's tolerant
// treatment of any missing/invalid capability input as "fall back to
// LeadTimePolicy" rather than a hard failure.
func computeNextCutoffs(sched scheduleData, from time.Time, n int) ([]order.CPTWindow, error) {
	if n <= 0 {
		return nil, nil
	}
	loc, err := time.LoadLocation(sched.Timezone)
	if err != nil {
		return nil, fmt.Errorf("kafkacptschedule: load timezone %q: %w", sched.Timezone, err)
	}

	type candidate struct {
		window order.CPTWindow
		at     time.Time
	}
	var candidates []candidate

	localFrom := from.In(loc)
	startOfToday := time.Date(localFrom.Year(), localFrom.Month(), localFrom.Day(), 0, 0, 0, 0, loc)

	for _, cutoff := range sched.Cutoffs {
		hour, minute, perr := parseLocalTime(cutoff.LocalTime)
		if perr != nil {
			return nil, fmt.Errorf("kafkacptschedule: cutoff %q: %w", cutoff.CptId, perr)
		}
		days := daySet(cutoff.DaysOfWeek)

		for offset := 0; offset <= cptSearchHorizonDays; offset++ {
			day := startOfToday.AddDate(0, 0, offset)
			if len(days) > 0 && !days[weekdayAbbrev[day.Weekday()]] {
				continue
			}
			occurrence := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc)
			if !occurrence.After(from) {
				continue
			}
			candidates = append(candidates, candidate{
				window: order.CPTWindow{
					CptId:           cutoff.CptId,
					CutoffAt:        occurrence,
					EligiblePathIds: append([]string(nil), cutoff.EligiblePathIds...),
				},
				at: occurrence,
			})
		}
	}

	sort.Slice(candidates, func(i, j int) bool { return candidates[i].at.Before(candidates[j].at) })

	if len(candidates) > n {
		candidates = candidates[:n]
	}
	out := make([]order.CPTWindow, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.window)
	}
	return out, nil
}

// parseLocalTime parses a strict "HH:MM" 24h local time.
func parseLocalTime(s string) (hour, minute int, err error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid local_time %q: %w", s, err)
	}
	return t.Hour(), t.Minute(), nil
}

// daySet builds a lookup set from the wire's []string days-of-week. An
// empty input means "every day" (no filtering), matching the fleet's
// convention elsewhere of an empty/absent constraint being permissive.
func daySet(days []string) map[string]bool {
	if len(days) == 0 {
		return nil
	}
	out := make(map[string]bool, len(days))
	for _, d := range days {
		out[d] = true
	}
	return out
}
