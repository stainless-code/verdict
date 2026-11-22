Report a `std::sync` guard alive at an `.await` — a `MutexGuard` or
`RwLockReadGuard` whose scope spans the suspension point. It makes the future
`!Send` and can deadlock the executor; async code needs `tokio::sync` or a
narrower scope.
