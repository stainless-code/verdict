package readgolden_test

import "testing"

const waitStubScript = `#!/usr/bin/env bash
case "$*" in
 "api repos/acme/app/pulls/42 --include")
 printf 'HTTP/2.0 200 OK\r\n\r\n%s' '{"state":"open","head":{"sha":"abc"}}';;
 "pr view 42 --repo acme/app --json headRefOid,statusCheckRollup,reviews")
 printf '%s' '{"headRefOid":"abc","statusCheckRollup":[{"__typename":"CheckRun","status":"COMPLETED","conclusion":"SUCCESS"}],"reviews":[]}';;
 *"api graphql"*)
 printf '%s' '{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}}}';;
 *) exit 64;;
esac
`

func TestPRWaitGolden(t *testing.T) {
	env := writeStub(t, waitStubScript, nil)
	goldenRead(t, "pr-wait/once-json", nil, env, nil, splitRepo("pr-wait", "42", "--until", "merged", "--once", "--json")...)
	goldenRead(t, "pr-wait/once-lines", nil, env, nil, splitRepo("pr-wait", "42", "--until", "checks-done", "--once")...)
}
