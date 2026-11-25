package lsp

import (
	"context"
	"sync"
	"sync/atomic"
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
	// expire, when set, replaces the wall-clock budget: the wait for key's
	// walk runs out when the returned channel closes, and a nil channel never
	// runs out. Tests set it so a walk times out because it is hung, never
	// because the machine is slow.
	expire  func(key string) <-chan struct{}
	budget  time.Duration
	limit   int
	running int
	mu      sync.Mutex
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

// miss says why a walk gave no answer: the callers' log lines differ, since
// a full slot table is not a slow volume.
type miss int

const (
	missNone   miss = iota
	missBudget      // the walk ran past the budget
	missNoSlot      // every walk slot was held, so no walk was started
)

// run returns fn's result for key if it finishes within the budget. A miss
// says it did not, or that no walk slot was free: the caller falls back and
// retries later, while the walk, if started, runs on and frees its slot when
// the filesystem answers.
func (r *resolver) run(key string, fn func() (string, bool)) (result string, ok bool, why miss) {
	w, started := r.start(key, fn)
	if !started {
		return "", false, missNoSlot
	}
	expired, stop := r.deadline(key)
	defer stop()
	select {
	case <-w.done:
		return w.result, w.ok, missNone
	case <-expired:
		return "", false, missBudget
	}
}

// each calls fn(0) .. fn(n-1), at most limit at a time, and returns when all
// have returned. fn writes its own index's result, so the outputs keep input
// order whatever order the walks finish in. Capping the callers at the slot
// count means a batch of N hung walks costs about ceil(N/limit) budgets and
// never starves itself of slots; a walk still holding a slot from an earlier
// hang is the only way a caller sees missNoSlot.
func (r *resolver) each(n int, fn func(i int)) {
	var next atomic.Int64
	var wg sync.WaitGroup
	for range min(n, r.limit) {
		wg.Go(func() { drainIndexes(&next, n, fn) })
	}
	wg.Wait()
}

// drainIndexes claims indexes from next until n and calls fn on each.
func drainIndexes(next *atomic.Int64, n int, fn func(i int)) {
	for i := int(next.Add(1)) - 1; i < n; i = int(next.Add(1)) - 1 {
		fn(i)
	}
}

// deadline starts the wait for key's walk: the channel closes when the
// budget runs out, and stop releases its timer.
func (r *resolver) deadline(key string) (<-chan struct{}, context.CancelFunc) {
	if r.expire != nil {
		return r.expire(key), func() {}
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.budget)
	return ctx.Done(), cancel
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
