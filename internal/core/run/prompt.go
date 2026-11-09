package run

import "fmt"

// directive is the tool-generated prompt the launched agent receives: the
// subject, the bundle it reads, the single review file it must write, and the
// boundary (verdict validates that file; it never reads prose, retries, or
// posts).
//
// It carries no bundle bytes on purpose. The agent reads the bundle with its
// own tools, so nothing attacker-influenced enters the prompt, no privacy
// exclusion is re-resolved at launch time, and the command --print renders
// stays byte-identical for every run of the same bundle. Handing an agent the
// whole fenced dump is `fetch --show-prompts`, a separate, fetch-time decision.
func directive(repo, pr, bundle, artifact string, local bool) string {
	subject := fmt.Sprintf("%s PR %s", repo, pr)
	meta := "PR.json"
	if local {
		subject = fmt.Sprintf("%s local change %s", repo, pr)
		meta = "source.json"
	}
	return fmt.Sprintf(`Review %s and write exactly one review file.

Read the installed verdict review skill first, then the bundle at %s: %s, roles.txt, and toc.txt indexing full.diff, plus kb.txt, sensitive.txt, and panel when the run carries them.

Write %s with {"schema_version": "1", "body": ..., "approved": ..., "comments": [...]} (optional "resolved" and "triage"). Write nothing else in the bundle.

verdict launched this one process and validates that file; it does not read your prose, retry you, or post. Do not post, merge, or push: publishing is an explicit verdict post.
`, subject, bundle, meta, artifact)
}
