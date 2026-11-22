Report `unwrap`, `expect`, or `panic!` on a fallible boundary the process cannot
recover from: parsing request input, reading a file, locking a poisoned mutex,
or indexing data sized by an external source. Report the same defect when `?`
propagation is skipped and a client error becomes a crash.
