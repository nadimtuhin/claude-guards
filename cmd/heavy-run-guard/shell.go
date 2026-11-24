package main

import "strings"

type heredoc struct {
	delimiter string
	indented  bool
}

// segment is one simple command of a shell script.
type segment struct {
	words []string
	// topLevel is true only when the command certainly runs in the invoking shell itself: not in
	// a subshell, substitution, pipeline or background job, not after && or ||, and not inside a
	// compound command (if, while, until, for, case, select, { }).
	topLevel bool
}

// compoundOpeners and compoundClosers start and end compound commands in command position.
var (
	compoundOpeners = map[string]bool{"if": true, "while": true, "until": true, "for": true, "case": true, "select": true, "{": true}
	compoundClosers = map[string]bool{"fi": true, "done": true, "esac": true, "}": true}
	bodyKeywords    = map[string]bool{"then": true, "do": true, "else": true, "elif": true, "!": true}
)

// splitSegments breaks a shell command into segments (split on && || ; | and newlines) of unquoted
// words. Quoted text stays inside its word and heredoc bodies are dropped, so only words the
// shell would actually execute or pass as arguments are left.
func splitSegments(command string) []segment {
	var segments []segment
	var words []string
	var word strings.Builder
	var pending []heredoc
	var nested []segment
	inWord := false
	// why: an export only persists when it runs unconditionally in this shell, so each segment
	// records the separators around it and how deeply it is nested.
	prevSep, parenDepth, blockDepth, inBacktick := "", 0, 0, false

	endWord := func() {
		if inWord {
			words = append(words, word.String())
		}
		word.Reset()
		inWord = false
	}
	endSegment := func(nextSep string) {
		endWord()
		if len(words) > 0 {
			topLevel := parenDepth == 0 && blockDepth == 0 && !inBacktick &&
				(prevSep == "" || prevSep == ";" || prevSep == "\n") &&
				(nextSep == "" || nextSep == ";" || nextSep == "\n" || nextSep == "&&" || nextSep == "||")
			segments = append(segments, segment{words, topLevel})
			// why: a compound command can open after a keyword (then { ...; }, do if ...), so look
			// past leading keywords for the opener, or its closer would end the outer block early.
			first := 0
			for first < len(words)-1 && bodyKeywords[words[first]] {
				first++
			}
			switch {
			case compoundOpeners[words[first]]:
				blockDepth++
			case compoundClosers[words[first]] && blockDepth > 0:
				blockDepth--
			}
		}
		words = nil
		prevSep = nextSep
	}

	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case c == '\'':
			inWord = true
			for i++; i < len(runes) && runes[i] != '\''; i++ {
				word.WriteRune(runes[i])
			}
		case c == '"':
			inWord = true
			for i++; i < len(runes) && runes[i] != '"'; i++ {
				if runes[i] == '\\' && i+1 < len(runes) {
					i++
				} else if body, end, ok := substitutionAt(runes, i); ok {
					// why: $(...) and `...` inside double quotes still run on this machine.
					for _, s := range splitSegments(body) {
						nested = append(nested, segment{s.words, false})
					}
					word.WriteString(string(runes[i : end+1]))
					i = end
					continue
				}
				word.WriteRune(runes[i])
			}
		case c == '\\' && i+1 < len(runes):
			inWord = true
			i++
			word.WriteRune(runes[i])
		case c == '<' && i+1 < len(runes) && runes[i+1] == '<' && !(i+2 < len(runes) && runes[i+2] == '<'):
			endWord()
			var h heredoc
			i += 2
			if i < len(runes) && runes[i] == '-' {
				h.indented = true
				i++
			}
			for ; i < len(runes) && (runes[i] == ' ' || runes[i] == '\t'); i++ {
			}
			for ; i < len(runes) && !strings.ContainsRune(" \t\n;&|<>()", runes[i]); i++ {
				if !strings.ContainsRune("'\"\\", runes[i]) {
					h.delimiter += string(runes[i])
				}
			}
			i--
			pending = append(pending, h)
		case c == '\n':
			endSegment("\n")
			rest := string(runes[i+1:])
			consumed := 0
			for _, h := range pending {
				rest, consumed = skipHeredocBody(rest, h, consumed)
			}
			i += consumed
			pending = nil
		case c == ';' || c == '(' || c == ')' || c == '`':
			// why: subshells, command substitutions and grouping start a new command.
			endSegment(string(c))
			switch {
			case c == '(':
				parenDepth++
			case c == ')' && parenDepth > 0:
				parenDepth--
			case c == '`':
				inBacktick = !inBacktick
			}
		case (c == '&' || c == '|') && i+1 < len(runes) && runes[i+1] == c:
			endSegment(string([]rune{c, c}))
			i++
		case c == '|' || c == '&':
			endSegment(string(c))
		case c == ' ' || c == '\t':
			endWord()
		default:
			inWord = true
			word.WriteRune(c)
		}
	}
	endSegment("")
	return append(segments, nested...)
}

// substitutionAt reports whether a command substitution starts at runes[i] and returns its body
// and the index of its closing rune.
func substitutionAt(runes []rune, i int) (string, int, bool) {
	if runes[i] == '`' {
		for j := i + 1; j < len(runes); j++ {
			if runes[j] == '`' {
				return string(runes[i+1 : j]), j, true
			}
		}
		return "", 0, false
	}
	if runes[i] != '$' || i+1 >= len(runes) || runes[i+1] != '(' {
		return "", 0, false
	}
	depth := 0
	for j := i + 1; j < len(runes); j++ {
		switch runes[j] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return string(runes[i+2 : j]), j, true
			}
		}
	}
	return "", 0, false
}

// skipHeredocBody returns rest without the lines up to and including the terminator, and the
// running count of runes consumed from the original text.
func skipHeredocBody(rest string, h heredoc, consumed int) (string, int) {
	for rest != "" {
		line, tail, _ := strings.Cut(rest, "\n")
		consumed += len([]rune(rest)) - len([]rune(tail))
		rest = tail
		if h.indented {
			line = strings.TrimLeft(line, "\t")
		}
		if line == h.delimiter {
			break
		}
	}
	return rest, consumed
}
