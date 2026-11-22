Report shared mutable state the change introduces or newly reaches concurrently:
a map or slice written from more than one goroutine without a lock, a
`sync.WaitGroup.Add` after its `go`, a loop variable captured by a goroutine or
deferred call, or a field read while another goroutine writes it with no
synchronisation.
