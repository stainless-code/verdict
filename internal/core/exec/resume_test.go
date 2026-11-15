package exec

import (
	"slices"
	"testing"
)

func TestResumePrimaryThreadRetriesAMissedThread(t *testing.T) {
	reads := 0
	lookup := func() ([]uint32, error) {
		reads++
		if reads == 1 {
			return nil, nil
		}
		return []uint32{4242}, nil
	}
	var resumed []uint32
	err := resumePrimaryThread(lookup, func(id uint32) bool {
		resumed = append(resumed, id)
		return true
	})
	if err != nil {
		t.Fatalf("resumePrimaryThread = %v; want nil once the thread is found", err)
	}
	if reads != 2 {
		t.Errorf("reads = %d; want 2", reads)
	}
	if want := []uint32{4242}; !slices.Equal(resumed, want) {
		t.Errorf("resumed threads = %v; want %v", resumed, want)
	}
}

func TestResumePrimaryThreadRetriesAFailedResume(t *testing.T) {
	attempts := 0
	err := resumePrimaryThread(
		func() ([]uint32, error) { return []uint32{77}, nil },
		func(uint32) bool {
			attempts++
			return attempts > 1
		},
	)
	if err != nil {
		t.Fatalf("resumePrimaryThread = %v; want nil once the resume takes", err)
	}
	if attempts != 2 {
		t.Errorf("resume attempts = %d; want 2", attempts)
	}
}

func TestResumePrimaryThreadRetriesASnapshotError(t *testing.T) {
	reads := 0
	lookup := func() ([]uint32, error) {
		reads++
		if reads == 1 {
			return nil, watchdogError("snapshot verify process threads: bad length")
		}
		return []uint32{9}, nil
	}
	if err := resumePrimaryThread(lookup, func(uint32) bool { return true }); err != nil {
		t.Fatalf("resumePrimaryThread = %v; want nil once the snapshot is readable", err)
	}
	if reads != 2 {
		t.Errorf("reads = %d; want 2", reads)
	}
}

func TestResumePrimaryThreadGivesUpAfterThreeReads(t *testing.T) {
	reads := 0
	err := resumePrimaryThread(
		func() ([]uint32, error) { reads++; return nil, nil },
		func(uint32) bool { return true },
	)
	if got, want := err.Error(), "resume suspended verify process: no thread resumed"; got != want {
		t.Errorf("error = %q; want %q", got, want)
	}
	if reads != 3 {
		t.Errorf("reads = %d; want 3", reads)
	}
}
