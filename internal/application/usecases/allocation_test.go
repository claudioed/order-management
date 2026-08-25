package usecases_test

import (
	"testing"

	"github.com/claudioed/order-management/internal/application/usecases"
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
