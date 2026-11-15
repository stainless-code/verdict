package exec

import "time"

const threadReadAttempts = 3

const threadReadPause = 5 * time.Millisecond

var errResumeFailed = watchdogError("resume suspended verify process: no thread resumed")

// Retries are free: the child is still suspended and has run no user code.
func resumePrimaryThread(lookup func() ([]uint32, error), resume func(uint32) bool) error {
	var last error = errResumeFailed
	for attempt := range threadReadAttempts {
		if attempt > 0 {
			time.Sleep(threadReadPause)
		}
		running, err := resumeOnce(lookup, resume)
		if running {
			return nil
		}
		last = err
	}
	return last
}

func resumeOnce(lookup func() ([]uint32, error), resume func(uint32) bool) (bool, error) {
	ids, err := lookup()
	if err != nil {
		return false, err
	}
	resumed := false
	for _, id := range ids {
		if resume(id) {
			resumed = true
		}
	}
	if !resumed {
		return false, errResumeFailed
	}
	return true, nil
}
