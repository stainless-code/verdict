Every `os.Open`, HTTP response body, `sql.Rows`, and file handle needs `Close`
on every return path, deferred at the acquisition site. Report a close that runs
only on the success path, a `defer` inside a loop that accumulates handles, or
an error branch that returns before the deferred cleanup is installed.
