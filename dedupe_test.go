package splatty

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestDedupeMarksOnlyOnce(t *testing.T) {
	d := newDedupeSet(8)
	err := errors.New("boom")

	if !d.markIfNew(err) {
		t.Errorf("first markIfNew = false, want true")
	}
	if d.markIfNew(err) {
		t.Errorf("second markIfNew = true, want false")
	}
	if !d.markIfNew(errors.New("boom")) {
		t.Errorf("a distinct error was treated as a duplicate")
	}
}

type valueError struct{ msg string }

func (v valueError) Error() string { return v.msg }

func TestDedupeAlwaysCapturesValueErrors(t *testing.T) {
	d := newDedupeSet(8)
	err := valueError{msg: "boom"}

	// A comparable value type has no stable identity, so it is never suppressed.
	if !d.markIfNew(err) || !d.markIfNew(err) {
		t.Errorf("value-typed errors must always be captured")
	}
}

func TestDedupeEvictsOldestPastTheLimit(t *testing.T) {
	d := newDedupeSet(4)
	first := errors.New("first")

	if !d.markIfNew(first) {
		t.Fatalf("first error was not marked")
	}
	for i := 0; i < 4; i++ {
		if !d.markIfNew(fmt.Errorf("filler %d", i)) {
			t.Fatalf("filler %d was not marked", i)
		}
	}
	if !d.markIfNew(first) {
		t.Errorf("the oldest entry was not evicted, so it stayed suppressed forever")
	}
	if len(d.order) > d.limit {
		t.Errorf("retained %d errors, want at most %d", len(d.order), d.limit)
	}
}

func TestDedupeIsConcurrencySafe(t *testing.T) {
	d := newDedupeSet(64)
	err := errors.New("shared")

	var wg sync.WaitGroup
	results := make([]bool, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = d.markIfNew(err)
		}(i)
	}
	wg.Wait()

	var marked int
	for _, ok := range results {
		if ok {
			marked++
		}
	}
	if marked != 1 {
		t.Errorf("%d goroutines marked the error, want exactly 1", marked)
	}
}

func TestDedupeIgnoresNil(t *testing.T) {
	d := newDedupeSet(4)
	if _, ok := identity(nil); ok {
		t.Errorf("identity(nil) reported an identity")
	}
	if !d.markIfNew(nil) {
		t.Errorf("markIfNew(nil) = false; nil is filtered earlier, not here")
	}
}
