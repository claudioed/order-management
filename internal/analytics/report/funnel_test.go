package report_test

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/order-management/internal/analytics/report"
)

// fakeStore is an in-memory implementation of both report ports used to
// exercise report derivation from a synthetic event sequence. It is a test
// double local to this package: the production stores live in the
// analyticsstore outbound adapter.
type fakeStore struct {
	seen map[string]bool
	rows map[report.RowKey]*acc
}

// acc is the fake store's per-row accumulator, kept separate from the public
// report.Row so intermediate state never leaks into the read-model type.
type acc struct {
	ordersReceived           int
	ordersAllocated          int
	ordersPartiallyAllocated int
	ordersAllocationFailed   int
	ordersReleased           int
	ordersCancelled          int
	linesAllocated           int
	linesBackordered         int
	linesReleased            int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		seen: map[string]bool{},
		rows: map[report.RowKey]*acc{},
	}
}

func (s *fakeStore) row(k report.RowKey) *acc {
	r, ok := s.rows[k]
	if !ok {
		r = &acc{}
		s.rows[k] = r
	}
	return r
}

func (s *fakeStore) dup(eventId string) bool {
	if s.seen[eventId] {
		return true
	}
	s.seen[eventId] = true
	return false
}

func hourBucket(t time.Time) time.Time {
	return t.UTC().Truncate(time.Hour)
}

func (s *fakeStore) accFor(eventId, pathId string, at time.Time) *acc {
	if s.dup(eventId) {
		return nil
	}
	return s.row(report.RowKey{PathId: pathId, HourBucket: hourBucket(at)})
}

func (s *fakeStore) ApplyOrderReceived(_ context.Context, eventId, pathId string, at time.Time) error {
	if r := s.accFor(eventId, pathId, at); r != nil {
		r.ordersReceived++
	}
	return nil
}

func (s *fakeStore) ApplyOrderAllocated(_ context.Context, eventId, pathId string, at time.Time) error {
	if r := s.accFor(eventId, pathId, at); r != nil {
		r.ordersAllocated++
	}
	return nil
}

func (s *fakeStore) ApplyOrderPartiallyAllocated(_ context.Context, eventId, pathId string, at time.Time) error {
	if r := s.accFor(eventId, pathId, at); r != nil {
		r.ordersPartiallyAllocated++
	}
	return nil
}

func (s *fakeStore) ApplyOrderAllocationFailed(_ context.Context, eventId, pathId string, at time.Time) error {
	if r := s.accFor(eventId, pathId, at); r != nil {
		r.ordersAllocationFailed++
	}
	return nil
}

func (s *fakeStore) ApplyOrderReleased(_ context.Context, eventId, pathId string, at time.Time) error {
	if r := s.accFor(eventId, pathId, at); r != nil {
		r.ordersReleased++
	}
	return nil
}

func (s *fakeStore) ApplyOrderCancelled(_ context.Context, eventId, pathId string, at time.Time) error {
	if r := s.accFor(eventId, pathId, at); r != nil {
		r.ordersCancelled++
	}
	return nil
}

func (s *fakeStore) ApplyLineAllocated(_ context.Context, eventId, pathId string, at time.Time) error {
	if r := s.accFor(eventId, pathId, at); r != nil {
		r.linesAllocated++
	}
	return nil
}

func (s *fakeStore) ApplyLineBackordered(_ context.Context, eventId, pathId string, at time.Time) error {
	if r := s.accFor(eventId, pathId, at); r != nil {
		r.linesBackordered++
	}
	return nil
}

func (s *fakeStore) ApplyLineReleased(_ context.Context, eventId, pathId string, at time.Time) error {
	if r := s.accFor(eventId, pathId, at); r != nil {
		r.linesReleased++
	}
	return nil
}

func (s *fakeStore) Query(_ context.Context, q report.ReportQuery) (report.FunnelReport, error) {
	out := report.FunnelReport{}
	for k, r := range s.rows {
		if k.HourBucket.Before(q.From) || !k.HourBucket.Before(q.To) {
			continue
		}
		if q.PathId != "" && k.PathId != q.PathId {
			continue
		}
		out.Rows = append(out.Rows, report.Row{
			Key:                      k,
			OrdersReceived:           r.ordersReceived,
			OrdersAllocated:          r.ordersAllocated,
			OrdersPartiallyAllocated: r.ordersPartiallyAllocated,
			OrdersAllocationFailed:   r.ordersAllocationFailed,
			OrdersReleased:           r.ordersReleased,
			OrdersCancelled:          r.ordersCancelled,
			LinesAllocated:           r.linesAllocated,
			LinesBackordered:         r.linesBackordered,
			LinesReleased:            r.linesReleased,
		})
	}
	return out, nil
}

func (s *fakeStore) FreshnessLag(_ context.Context) (time.Duration, error) {
	return 0, nil
}

func TestFunnelReport_DerivesFromEventSequence(t *testing.T) {
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	s := newFakeStore()
	ctx := context.Background()

	// Synthetic funnel for the "pick" path in one hour bucket:
	//   received → allocated (2 lines) → released (2 lines),
	//   plus one partial allocation, one cancel, one line backorder.
	must(t, s.ApplyOrderReceived(ctx, "e1", "pick", base))
	must(t, s.ApplyLineAllocated(ctx, "e2", "pick", base.Add(time.Minute)))
	must(t, s.ApplyLineAllocated(ctx, "e3", "pick", base.Add(2*time.Minute)))
	must(t, s.ApplyOrderAllocated(ctx, "e4", "pick", base.Add(3*time.Minute)))
	must(t, s.ApplyLineReleased(ctx, "e5", "pick", base.Add(4*time.Minute)))
	must(t, s.ApplyLineReleased(ctx, "e6", "pick", base.Add(5*time.Minute)))
	must(t, s.ApplyOrderReleased(ctx, "e7", "pick", base.Add(6*time.Minute)))
	must(t, s.ApplyOrderPartiallyAllocated(ctx, "e8", "pick", base.Add(7*time.Minute)))
	must(t, s.ApplyLineBackordered(ctx, "e9", "pick", base.Add(8*time.Minute)))
	must(t, s.ApplyOrderCancelled(ctx, "e10", "pick", base.Add(9*time.Minute)))
	must(t, s.ApplyOrderAllocationFailed(ctx, "e11", "singles", base.Add(10*time.Minute)))

	rep, err := s.Query(ctx, report.ReportQuery{
		From:        base.Add(-time.Hour),
		To:          base.Add(2 * time.Hour),
		Granularity: report.GranularityHour,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	bucket := base.Truncate(time.Hour)
	pick := findRow(rep, report.RowKey{PathId: "pick", HourBucket: bucket})
	if pick == nil {
		t.Fatal("no pick row")
	}
	checks := []struct {
		name string
		got  int
		want int
	}{
		{"OrdersReceived", pick.OrdersReceived, 1},
		{"OrdersAllocated", pick.OrdersAllocated, 1},
		{"OrdersPartiallyAllocated", pick.OrdersPartiallyAllocated, 1},
		{"OrdersReleased", pick.OrdersReleased, 1},
		{"OrdersCancelled", pick.OrdersCancelled, 1},
		{"LinesAllocated", pick.LinesAllocated, 2},
		{"LinesReleased", pick.LinesReleased, 2},
		{"LinesBackordered", pick.LinesBackordered, 1},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}

	singles := findRow(rep, report.RowKey{PathId: "singles", HourBucket: bucket})
	if singles == nil || singles.OrdersAllocationFailed != 1 {
		t.Errorf("singles OrdersAllocationFailed = %v, want 1", singles)
	}
}

func TestFunnelReport_FiltersAndIdempotency(t *testing.T) {
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	ctx := context.Background()

	tests := []struct {
		name  string
		query report.ReportQuery
		want  int // number of rows expected
	}{
		{"no filter", report.ReportQuery{From: base.Add(-time.Hour), To: base.Add(time.Hour), Granularity: report.GranularityHour}, 2},
		{"path filter", report.ReportQuery{From: base.Add(-time.Hour), To: base.Add(time.Hour), PathId: "pick", Granularity: report.GranularityHour}, 1},
		{"window excludes all", report.ReportQuery{From: base.Add(24 * time.Hour), To: base.Add(48 * time.Hour), Granularity: report.GranularityHour}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newFakeStore()
			// Apply the same received event twice with the same eventId →
			// counts once.
			must(t, s.ApplyOrderReceived(ctx, "dup", "pick", base))
			must(t, s.ApplyOrderReceived(ctx, "dup", "pick", base))
			must(t, s.ApplyOrderReceived(ctx, "other", "singles", base))

			rep, err := s.Query(ctx, tt.query)
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if len(rep.Rows) != tt.want {
				t.Errorf("rows = %d, want %d", len(rep.Rows), tt.want)
			}
			if tt.name == "no filter" {
				pick := findRow(rep, report.RowKey{PathId: "pick", HourBucket: base.Truncate(time.Hour)})
				if pick == nil || pick.OrdersReceived != 1 {
					t.Errorf("dedupe failed: pick OrdersReceived = %v", pick)
				}
			}
		})
	}
}

func findRow(rep report.FunnelReport, k report.RowKey) *report.Row {
	for i := range rep.Rows {
		if rep.Rows[i].Key == k {
			return &rep.Rows[i]
		}
	}
	return nil
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
}
