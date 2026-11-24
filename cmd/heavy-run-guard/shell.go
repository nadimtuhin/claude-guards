package main

import "strings"

type heredoc struct {
	delimiter string
	indented  bool
}

// splitSegments breaks a shell command into segments (split on && || ; | and newlines) of unquoted
// words. Quoted text stays inside its word and heredoc bodies are dropped, so only words the
// shell would actually execute or pass as arguments are left.
func splitSegments(command string) [][]string {
	var segments [][]string
	var words []string
	var word strings.Builder
	var pending []heredoc
	var nested [][]string
	inWord := false

	endWord := func() {
		if inWord {
			words = append(words, word.String())
		}
		word.Reset()
		inWord = false
	}
	endSegment := func() {
		endWord()
		if len(words) > 0 {
			segments = append(segments, words)
		}
		words = nil
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
					nested = append(nested, splitSegments(body)...)
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
			endSegment()
			rest := string(runes[i+1:])
			consumed := 0
			for _, h := range pending {
				rest, consumed = skipHeredocBody(rest, h, consumed)
			}
			i += consumed
			pending = nil
		case c == ';' || c == '(' || c == ')' || c == '`':
			// why: subshells, command substitutions and grouping start a new command.
			endSegment()
		case (c == '&' || c == '|') && i+1 < len(runes) && runes[i+1] == c:
			endSegment()
			i++
		case c == '|' || c == '&':
			endSegment()
		case c == ' ' || c == '\t':
			endWord()
		default:
			inWord = true
			word.WriteRune(c)
		}
	}
	endSegment()
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
