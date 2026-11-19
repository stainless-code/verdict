package mcp

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"

	"github.com/stainless-code/verdict/internal/core"
	journalcore "github.com/stainless-code/verdict/internal/core/journal"
)

const (
	mcpExitOK      = 0
	mcpExitRefused = 1
	mcpExitBroke   = 3
)

func (s *Server) invokeJournaled(
	t *tool, b *bindings, counter *journalcore.CountingProgress,
	request any, rawArgs json.RawMessage,
	fn func() (*jqObjResult, *core.RefusalError),
) (*jqObjResult, *core.RefusalError) {
	repoRoot, pr := journalcore.TargetFromRequest(request)
	var (
		res *jqObjResult
		ref *core.RefusalError
	)
	journalcore.Guard(&journalcore.Invocation{
		Verb:     journalcore.CanonicalVerb(t.name),
		Channel:  journalcore.ChannelMCP,
		RepoRoot: repoRoot,
		Pr:       pr,
		Argv:     reqArgv(t.name, rawArgs),
		Environ:  s.cfg.Environ,
		Version:  s.cfg.Version,
		Now:      s.cfg.Now,
		Warn:     counter.Warn,
		Refusal:  func() string { return refusalCodeOf(ref) },
		Warnings: func() int { return mcpWarningCount(counter, b, ref) },
	}, func() int {
		res, ref = fn()
		return mcpExitCode(ref)
	})
	return res, ref
}

func refusalCodeOf(ref *core.RefusalError) string {
	if ref == nil {
		return ""
	}
	return ref.Code
}

func mcpWarningCount(counter *journalcore.CountingProgress, b *bindings, ref *core.RefusalError) int {
	n := counter.Count() + len(b.warnings)
	if ref != nil && len(ref.Warnings) > n {
		return len(ref.Warnings)
	}
	return n
}

func mcpExitCode(ref *core.RefusalError) int {
	if ref == nil {
		return mcpExitOK
	}
	if ref.Code == "canceled" {
		return mcpExitBroke
	}
	return mcpExitRefused
}

// reqArgv renders an MCP tool call's JSON arguments as a deterministic
// CLI-shaped argv slice so [argvshape.Record] applies the exact same secret
// redaction rules across both front doors.
func reqArgv(toolName string, raw json.RawMessage) []string {
	argv := []string{journalcore.CanonicalVerb(toolName)}
	var obj map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &obj) != nil {
		return argv
	}
	argv = appendMCPPositionals(argv, obj)
	for _, k := range slices.Sorted(maps.Keys(obj)) {
		if k != argRepo && k != argPr {
			argv = appendArgFlag(argv, flagNameFor(k), obj[k])
		}
	}
	return argv
}

func appendMCPPositionals(argv []string, obj map[string]any) []string {
	for _, key := range []string{argRepo, argPr} {
		if v, ok := obj[key].(string); ok && v != "" {
			argv = append(argv, v)
		}
	}
	return argv
}

func appendArgFlag(argv []string, flag string, val any) []string {
	switch v := val.(type) {
	case nil:
		return argv
	case bool:
		if v {
			return append(argv, flag)
		}
		return argv
	case string:
		return append(argv, flag, v)
	case []any:
		for _, elem := range v {
			argv = append(argv, flag, fmt.Sprint(elem))
		}
		return argv
	default:
		return append(argv, flag, fmt.Sprint(v))
	}
}

func flagNameFor(key string) string {
	var out strings.Builder
	out.WriteString("--")
	for i, r := range key {
		switch {
		case r == '_':
			out.WriteByte('-')
		case unicode.IsUpper(r):
			if i > 0 {
				out.WriteByte('-')
			}
			out.WriteRune(unicode.ToLower(r))
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}
