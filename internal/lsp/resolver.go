package lsp

import (
	"sync"
	"time"
)

const (
	// resolveBudget is how long a notification waits for a filesystem walk
	// (resolving a path's physical spelling, probing a root's checkout)
	// before going on without it.
	resolveBudget = 250 * time.Millisecond
	// maxResolvers bounds the walks outstanding at once. A walk on a hung
	// volume cannot be canceled (the syscall blocks), so each stuck walk
	// holds a slot until the volume answers; past the bound a new walk is
	// not started and its caller goes on as if it had timed out.
	maxResolvers = 8
)

// resolver runs filesystem walks off the caller's goroutine with a deadline.
// Callers asking for the same key while a walk is in flight share it, so a
// hung path costs one goroutine, not one per notification.
type resolver struct {
	inflight map[string]*walk
	budget   time.Duration
	limit    int
	running  int
	mu       sync.Mutex
}

// walk is one in-flight filesystem walk and, once done is closed, its result.
type walk struct {
	done   chan struct{}
	result string
	ok     bool
}

func newResolver(budget time.Duration, limit int) *resolver {
	return &resolver{inflight: map[string]*walk{}, budget: budget, limit: limit}
}

// run returns fn's result for key if it finishes within the budget. timedOut
// reports that it did not (or that no walk slot was free): the caller falls
// back and retries later, while the walk, if started, runs on and frees its
// slot when the filesystem answers.
func (r *resolver) run(key string, fn func() (string, bool)) (result string, ok, timedOut bool) {
	w, started := r.start(key, fn)
	if !started {
		return "", false, true
	}
	t := time.NewTimer(r.budget)
	defer t.Stop()
	select {
	case <-w.done:
		return w.result, w.ok, false
	case <-t.C:
		return "", false, true
	}
}

// start joins the walk in flight for key, or starts one when a slot is free.
func (r *resolver) start(key string, fn func() (string, bool)) (*walk, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if w := r.inflight[key]; w != nil {
		return w, true
	}
	if r.running >= r.limit {
		return nil, false
	}
	w := &walk{done: make(chan struct{})}
	r.inflight[key] = w
	r.running++
	go func() {
		result, ok := fn()
		r.mu.Lock()
		w.result, w.ok = result, ok
		delete(r.inflight, key)
		r.running--
		r.mu.Unlock()
		close(w.done)
	}()
	return w, true
}
