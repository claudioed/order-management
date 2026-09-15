package usecases_test

import (
	"testing"

	"github.com/claudioed/order-management/internal/application/usecases"
	"github.com/claudioed/order-management/internal/domain/shared"
)

func TestWorkUnitIDIsDeterministicAndLineScoped(t *testing.T) {
	if got, want := usecases.WorkUnitID("ord-77213", 1), "ord-77213-line-1"; got != want {
		t.Fatalf("WorkUnitID = %q, want %q", got, want)
	}
	if usecases.WorkUnitID("ord-1", 1) == usecases.WorkUnitID("ord-1", 2) {
		t.Fatal("two lines of the same order must not share a work unit id")
	}
	if usecases.WorkUnitID("ord-1", 1) == usecases.WorkUnitID("ord-2", 1) {
		t.Fatal("two orders must not share a work unit id")
	}
}

// TestParseWorkUnitID_RoundTripsWithWorkUnitID pins the exact contract
// ADR 0018 depends on: for every OrderId/lineNo WorkUnitID can produce,
// ParseWorkUnitID must recover them byte-for-byte. This is the
// round-trip a gremlins string-splitting mutant is most likely to break
// silently.
func TestParseWorkUnitID_RoundTripsWithWorkUnitID(t *testing.T) {
	tests := []struct {
		orderID shared.OrderId
		lineNo  int
	}{
		{"ord-7c9e6679-7d5a-4b37-b2f1-93b0c4a1d8f2", 1},
		{"ord-7c9e6679-7d5a-4b37-b2f1-93b0c4a1d8f2", 2},
		{"ord-1", 1},
		{"ord-1", 42},
	}
	for _, tt := range tests {
		wire := usecases.WorkUnitID(tt.orderID, tt.lineNo)
		gotOrderID, gotLineNo, ok := usecases.ParseWorkUnitID(wire)
		if !ok {
			t.Fatalf("ParseWorkUnitID(%q) ok = false, want true", wire)
		}
		if gotOrderID != tt.orderID {
			t.Errorf("ParseWorkUnitID(%q) orderID = %q, want %q", wire, gotOrderID, tt.orderID)
		}
		if gotLineNo != tt.lineNo {
			t.Errorf("ParseWorkUnitID(%q) lineNo = %d, want %d", wire, gotLineNo, tt.lineNo)
		}
	}
}

// TestParseWorkUnitID_LastOccurrenceIsSafeAgainstAnOrderIdContainingTheMarker
// pins the deliberate "split on the LAST -line- occurrence" choice: even
// an (unrealistic today, but not impossible) order id that itself
// contains the literal "-line-" substring must still resolve to the
// TRAILING numeric suffix as the line number, not an earlier one.
func TestParseWorkUnitID_LastOccurrenceIsSafeAgainstAnOrderIdContainingTheMarker(t *testing.T) {
	orderID, lineNo, ok := usecases.ParseWorkUnitID("ord-weird-line-thing-line-3")
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if orderID != "ord-weird-line-thing" {
		t.Errorf("orderID = %q, want %q", orderID, "ord-weird-line-thing")
	}
	if lineNo != 3 {
		t.Errorf("lineNo = %d, want 3", lineNo)
	}
}

// TestParseWorkUnitID_MalformedInputNeverPanicsAndReportsNotOK covers
// every shape of malformed order_ref this consumer must tolerate per ADR
// 0018: no marker at all, a marker with nothing before/after it, a
// non-numeric or non-positive line number. Every case must degrade to
// ok=false, never a panic.
func TestParseWorkUnitID_MalformedInputNeverPanicsAndReportsNotOK(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"no marker at all", "ord-7c9e6679"},
		{"marker with empty order id before it", "-line-1"},
		{"marker with nothing after it", "ord-1-line-"},
		{"non-numeric line number", "ord-1-line-abc"},
		{"zero line number", "ord-1-line-0"},
		{"negative line number", "ord-1-line--1"},
		{"float line number", "ord-1-line-1.5"},
		{"trailing whitespace on line number", "ord-1-line-1 "},
		{"marker only, no order id, no line number", "-line-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("ParseWorkUnitID(%q) panicked: %v", tt.input, r)
				}
			}()
			orderID, lineNo, ok := usecases.ParseWorkUnitID(tt.input)
			if ok {
				t.Fatalf("ParseWorkUnitID(%q) = (%q, %d, true), want ok=false", tt.input, orderID, lineNo)
			}
			if orderID != "" || lineNo != 0 {
				t.Fatalf("ParseWorkUnitID(%q) on ok=false must zero its outputs, got (%q, %d)", tt.input, orderID, lineNo)
			}
		})
	}
}
