package calibratecore

import threadscore "github.com/stainless-code/verdict/internal/core/threads"

func feedNodes(feed *threadscore.File) []any {
	if feed == nil {
		return nil
	}
	nodes := make([]any, 0, len(feed.Threads))
	for i := range feed.Threads {
		nodes = append(nodes, feedNode(feed.Provider, &feed.Threads[i]))
	}
	return nodes
}

func feedNode(provider string, thread *threadscore.Thread) any {
	comments := make([]any, 0, len(thread.Comments))
	for i := range thread.Comments {
		comment := &thread.Comments[i]
		if provider == providerGitLab {
			comments = append(comments, map[string]any{
				"body":     comment.Body,
				"position": map[string]any{"new_path": thread.Path},
				"resolved": thread.Resolved,
				"system":   false,
			})
			continue
		}
		comments = append(comments, map[string]any{"body": comment.Body, "path": thread.Path})
	}
	if provider == providerGitLab {
		return map[string]any{"notes": comments}
	}
	return map[string]any{
		"isResolved": thread.Resolved,
		"comments":   map[string]any{"nodes": comments},
	}
}

// StoredOutcome carries the dimensions and result used by the serve view.
type StoredOutcome struct {
	Category string
	Severity string
	Outcome  string
}

// StoredOutcomes uses the calibrate live join with the stored thread feed and
// no changed-file evidence. Fixed cannot be measured offline.
func StoredOutcomes(entries []any, feed *threadscore.File) []StoredOutcome {
	provider := providerGitHub
	if feed != nil {
		provider = feed.Provider
	}
	host, ok := liveHostFor(provider)
	if !ok {
		host = liveHosts[providerGitHub]
		feed = nil
	}
	outcomes := joinOutcomes(entries, feedNodes(feed), nil, host)
	rows := make([]StoredOutcome, 0, len(outcomes))
	for i := range outcomes {
		rows = append(rows, StoredOutcome{
			Category: outcomes[i].cat,
			Severity: outcomes[i].sev,
			Outcome:  outcomes[i].result,
		})
	}
	return rows
}
