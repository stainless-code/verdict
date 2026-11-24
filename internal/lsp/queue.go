package lsp

import "sync"

// jobQueue runs at most limit jobs at once and holds at most capacity more
// in FIFO order. A job submitted past both bounds is refused, never dropped
// silently: the caller answers the request it carried. Every accepted job
// runs exactly once, on its own goroutine.
type jobQueue struct {
	waiting  []func()
	limit    int
	capacity int
	running  int
	mu       sync.Mutex
}

// submit starts job now when a slot is free, queues it when the queue has
// room, and reports false when it does neither.
func (q *jobQueue) submit(job func()) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.running < q.limit {
		q.running++
		go q.run(job)
		return true
	}
	if len(q.waiting) >= q.capacity {
		return false
	}
	q.waiting = append(q.waiting, job)
	return true
}

// run executes job, then hands its slot to the oldest waiting job.
func (q *jobQueue) run(job func()) {
	for job != nil {
		job()
		job = q.next()
	}
}

// next pops the oldest waiting job, or releases the slot when none waits.
func (q *jobQueue) next() func() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.waiting) == 0 {
		q.running--
		return nil
	}
	job := q.waiting[0]
	q.waiting[0] = nil
	q.waiting = q.waiting[1:]
	return job
}

// counts reports the running and waiting jobs, for tests and logs.
func (q *jobQueue) counts() (running, waiting int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.running, len(q.waiting)
}
