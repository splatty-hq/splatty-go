package splatty

import (
	"reflect"
	"sync"
)

const dedupeLimit = 1024

// dedupeSet remembers which error values have already been reported, so a
// failure that surfaces through two integrations produces one event. Go has no
// weak references, so the set is bounded and evicts oldest-first; it keeps the
// errors themselves alive, which stops a freed address from being reused and
// silently suppressing an unrelated error.
type dedupeSet struct {
	mu    sync.Mutex
	seen  map[uintptr]struct{}
	order []error
	limit int
}

func newDedupeSet(limit int) *dedupeSet {
	if limit <= 0 {
		limit = dedupeLimit
	}
	return &dedupeSet{seen: make(map[uintptr]struct{}, limit), limit: limit}
}

// markIfNew reports whether err should be captured, marking it as seen.
// Errors with no stable identity (comparable value types) are always captured.
func (d *dedupeSet) markIfNew(err error) bool {
	id, ok := identity(err)
	if !ok {
		return true
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if _, dup := d.seen[id]; dup {
		return false
	}

	if len(d.order) >= d.limit {
		oldest := d.order[0]
		d.order = d.order[1:]
		if oldID, ok := identity(oldest); ok {
			delete(d.seen, oldID)
		}
	}
	d.seen[id] = struct{}{}
	d.order = append(d.order, err)
	return true
}

func identity(err error) (uintptr, bool) {
	if err == nil {
		return 0, false
	}
	v := reflect.ValueOf(err)
	switch v.Kind() {
	case reflect.Pointer, reflect.UnsafePointer, reflect.Chan, reflect.Map, reflect.Func:
		if v.IsNil() {
			return 0, false
		}
		return v.Pointer(), true
	default:
		return 0, false
	}
}
