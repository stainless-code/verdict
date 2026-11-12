package verdict

import "context"

// SetRepoRootForTest points c's repository-root resolution at fn, so a test
// whose subject is not the git top-level probe never spawns it: the probe runs
// under its own 5s bound, and a loaded machine reaches that on the spawn alone.
func SetRepoRootForTest(c *Client, fn func(context.Context) (string, error)) {
	c.repoRoot = fn
}
