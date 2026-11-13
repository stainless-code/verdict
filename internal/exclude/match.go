package exclude

type globState struct {
	pattern string
	s       string
	px      int
	sx      int
	star    int
	mark    int
}

// Match applies the KB shell-glob dialect: stars cross slashes; classes,
// question marks and backslash escapes match one byte.
func Match(pattern, s string) bool {
	g := &globState{pattern: pattern, s: s, star: -1}
	for g.sx < len(g.s) {
		if !g.step() {
			return false
		}
	}
	for g.px < len(g.pattern) && g.pattern[g.px] == '*' {
		g.px++
	}
	return g.px == len(g.pattern)
}

func (g *globState) step() bool {
	if g.matchEscape() {
		return true
	}
	if g.matchStar() {
		return true
	}
	if g.matchConsume() {
		return true
	}
	return g.matchBacktrack()
}

func (g *globState) matchEscape() bool {
	if g.px < len(g.pattern) && g.pattern[g.px] == '\\' && g.px+1 < len(g.pattern) && g.pattern[g.px+1] == g.s[g.sx] {
		g.px += 2
		g.sx++
		return true
	}
	return false
}

func (g *globState) matchStar() bool {
	if g.px < len(g.pattern) && g.pattern[g.px] == '*' {
		g.star, g.mark, g.px = g.px, g.sx, g.px+1
		return true
	}
	return false
}

func (g *globState) matchConsume() bool {
	if g.px >= len(g.pattern) {
		return false
	}
	switch g.pattern[g.px] {
	case '?':
		g.px++
		g.sx++
		return true
	case '[':
		if n, ok := matchClass(g.pattern[g.px:], g.s[g.sx]); ok {
			g.px += n
			g.sx++
			return true
		}
		return false
	default:
		if g.pattern[g.px] == g.s[g.sx] {
			g.px++
			g.sx++
			return true
		}
		return false
	}
}

func (g *globState) matchBacktrack() bool {
	if g.star == -1 || g.mark >= len(g.s) {
		return false
	}
	g.mark++
	g.px, g.sx = g.star+1, g.mark
	return true
}

func matchClass(pat string, c byte) (int, bool) {
	i, neg := classNeg(pat)
	if i < len(pat) && pat[i] == ']' {
		i++
	}
	matched, closed, end := classItems(pat, i, c)
	if !closed {
		return 0, false
	}
	if neg {
		matched = !matched
	}
	if !matched {
		return 0, false
	}
	return end, true
}

func classNeg(pat string) (int, bool) {
	if len(pat) > 1 && (pat[1] == '!' || pat[1] == '^') {
		return 2, true
	}
	return 1, false
}

func classItems(pat string, i int, c byte) (matched, closed bool, end int) {
	for i < len(pat) {
		if pat[i] == ']' {
			return matched, true, i + 1
		}
		var hit bool
		i, hit = classItem(pat, i, c)
		matched = matched || hit
	}
	return matched, false, i
}

func classItem(pat string, i int, c byte) (int, bool) {
	lo := pat[i]
	i++
	if lo == '\\' && i < len(pat) {
		lo = pat[i]
		i++
	}
	hi := lo
	if i+1 < len(pat) && pat[i] == '-' && pat[i+1] != ']' {
		hi = pat[i+1]
		i += 2
	}
	return i, lo <= c && c <= hi
}
