package gen

import (
	"regexp/syntax"
	"strings"
	"unicode"
)

var preferredRunes = []rune("ax0-.")

// fromPattern returns a string matching pattern, with every unbounded repetition expanded reps times.
func fromPattern(pattern string, reps int) (string, error) {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	writeRegex(&b, re.Simplify(), reps)
	return b.String(), nil
}

func writeRegex(b *strings.Builder, re *syntax.Regexp, reps int) {
	switch re.Op {
	case syntax.OpLiteral:
		for _, r := range re.Rune {
			if re.Flags&syntax.FoldCase != 0 {
				r = unicode.ToLower(r)
			}
			b.WriteRune(r)
		}
	case syntax.OpCharClass:
		b.WriteRune(pickRune(re.Rune))
	case syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		b.WriteRune('a')
	case syntax.OpCapture:
		writeRegex(b, re.Sub[0], reps)
	case syntax.OpConcat:
		for _, sub := range re.Sub {
			writeRegex(b, sub, reps)
		}
	case syntax.OpAlternate:
		writeRegex(b, re.Sub[0], reps)
	case syntax.OpStar:
		repeatRegex(b, re.Sub[0], reps, reps)
	case syntax.OpPlus:
		repeatRegex(b, re.Sub[0], max(1, reps), reps)
	case syntax.OpQuest:
		repeatRegex(b, re.Sub[0], min(1, reps), reps)
	case syntax.OpRepeat:
		n := max(re.Min, reps)
		if re.Max >= 0 {
			n = min(n, re.Max)
		}
		repeatRegex(b, re.Sub[0], n, reps)
	}
}

func repeatRegex(b *strings.Builder, re *syntax.Regexp, n, reps int) {
	for range n {
		writeRegex(b, re, reps)
	}
}

func pickRune(ranges []rune) rune {
	for _, want := range preferredRunes {
		for i := 0; i+1 < len(ranges); i += 2 {
			if ranges[i] <= want && want <= ranges[i+1] {
				return want
			}
		}
	}
	for i := 0; i+1 < len(ranges); i += 2 {
		if r := max(ranges[i], '!'); r <= ranges[i+1] {
			return r
		}
	}
	if len(ranges) > 0 {
		return ranges[0]
	}
	return 'a'
}
