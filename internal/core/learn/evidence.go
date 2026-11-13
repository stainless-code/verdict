package learncore

import (
	"io/fs"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/pack"
	threads "github.com/stainless-code/verdict/internal/core/threads"
)

const (
	learningPathMaxBytes      = 4096
	learningCommentIDMaxBytes = 256
)

const (
	msgLearningScope     = "learning cited path must be a clean repo-relative path of at most 4096 bytes without controls or glob syntax"
	msgLearningCommentID = "learning comment id must contain 1..256 ASCII letters, digits, underscores, hyphens or equals signs"
)

func checkLearningPath(file string) *core.RefusalError {
	if !validLearningPath(file) {
		return &core.RefusalError{Code: codeBadScope, Message: msgLearningScope}
	}
	return nil
}

func validLearningPath(file string) bool {
	if len(file) > learningPathMaxBytes || !utf8.ValidString(file) || !fs.ValidPath(file) || file == "." {
		return false
	}
	if path.Clean(file) != file || strings.ContainsAny(file, `\,:*?[]`) {
		return false
	}
	return !strings.ContainsFunc(file, unicode.IsControl)
}

func checkLearningCommentID(id string) *core.RefusalError {
	if len(id) == 0 || len(id) > learningCommentIDMaxBytes || strings.ContainsFunc(id, invalidLearningIDRune) {
		return &core.RefusalError{Code: codeBadScope, Message: msgLearningCommentID}
	}
	return nil
}

func invalidLearningIDRune(r rune) bool {
	return !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-=", r)
}

func checkLearningFeed(feed *threads.File) *core.RefusalError {
	for i := range feed.Threads {
		thread := &feed.Threads[i]
		if !thread.Resolved || thread.Outdated {
			continue
		}
		if ref := checkLearningThread(thread); ref != nil {
			return ref
		}
	}
	return nil
}

func checkLearningThread(thread *threads.Thread) *core.RefusalError {
	if ref := checkLearningPath(thread.Path); ref != nil {
		return ref
	}
	for _, comment := range thread.Comments {
		if ref := checkLearningCommentID(comment.ID); ref != nil {
			return ref
		}
	}
	return nil
}

func checkLearningRule(rule *pack.Rule) *core.RefusalError {
	for _, glob := range rule.Globs {
		if ref := checkLearningPath(glob); ref != nil {
			return ref
		}
	}
	id, ok := strings.CutPrefix(rule.Provenance, "threads#")
	if !ok {
		return &core.RefusalError{Code: codeBadScope, Message: "learning provenance must identify a validated threads comment"}
	}
	return checkLearningCommentID(id)
}
