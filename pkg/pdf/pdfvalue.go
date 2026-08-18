package pdf

import "bytes"

// isPDFWhitespace reports whether b is whitespace per the PDF spec (Table 1).
func isPDFWhitespace(b byte) bool {
	switch b {
	case 0x00, '\t', '\n', '\f', '\r', ' ':
		return true
	}
	return false
}

// isPDFDelim reports whether b is a delimiter character per the PDF spec.
func isPDFDelim(b byte) bool {
	return bytes.IndexByte([]byte("()<>[]{}/%"), b) >= 0
}

// skipStringLiteral returns the index right after the matching closing ')'
// for a literal string starting at buf[pos] == '('. It respects nested,
// unescaped parentheses and backslash escapes.
func skipStringLiteral(buf []byte, pos int) int {
	depth := 1
	pos++
	for pos < len(buf) && depth > 0 {
		switch buf[pos] {
		case '\\':
			pos++
		case '(':
			depth++
		case ')':
			depth--
		}
		pos++
	}
	return pos
}

// readValueAt reads one PDF value (name, array, dictionary, string, number,
// boolean, null, or an indirect reference "N G R") starting at or after pos
// (leading whitespace is skipped). It returns the byte span [start,end) of
// the value within buf.
func readValueAt(buf []byte, pos int) (start, end int, ok bool) {
	for pos < len(buf) && isPDFWhitespace(buf[pos]) {
		pos++
	}
	if pos >= len(buf) {
		return 0, 0, false
	}
	start = pos

	switch buf[pos] {
	case '/':
		pos++
		for pos < len(buf) && !isPDFDelim(buf[pos]) && !isPDFWhitespace(buf[pos]) {
			pos++
		}
		return start, pos, true

	case '[':
		depth := 0
		for pos < len(buf) {
			switch buf[pos] {
			case '(':
				pos = skipStringLiteral(buf, pos)
				continue
			case '[':
				depth++
			case ']':
				depth--
				if depth == 0 {
					pos++
					return start, pos, true
				}
			}
			pos++
		}
		return 0, 0, false

	case '<':
		if pos+1 < len(buf) && buf[pos+1] == '<' {
			depth := 0
			for pos < len(buf) {
				if buf[pos] == '<' && pos+1 < len(buf) && buf[pos+1] == '<' {
					depth++
					pos += 2
					continue
				}
				if buf[pos] == '>' && pos+1 < len(buf) && buf[pos+1] == '>' {
					depth--
					pos += 2
					if depth == 0 {
						return start, pos, true
					}
					continue
				}
				pos++
			}
			return 0, 0, false
		}
		pos++
		for pos < len(buf) && buf[pos] != '>' {
			pos++
		}
		if pos >= len(buf) {
			return 0, 0, false
		}
		pos++
		return start, pos, true

	case '(':
		pos = skipStringLiteral(buf, pos)
		return start, pos, true

	default:
		p1 := pos
		for p1 < len(buf) && !isPDFDelim(buf[p1]) && !isPDFWhitespace(buf[p1]) {
			p1++
		}
		if p1 == pos {
			return 0, 0, false
		}
		if isUint(buf[pos:p1]) {
			p2 := p1
			for p2 < len(buf) && isPDFWhitespace(buf[p2]) {
				p2++
			}
			p3 := p2
			for p3 < len(buf) && !isPDFDelim(buf[p3]) && !isPDFWhitespace(buf[p3]) {
				p3++
			}
			if p3 > p2 && isUint(buf[p2:p3]) {
				p4 := p3
				for p4 < len(buf) && isPDFWhitespace(buf[p4]) {
					p4++
				}
				if p4 < len(buf) && buf[p4] == 'R' &&
					(p4+1 >= len(buf) || isPDFDelim(buf[p4+1]) || isPDFWhitespace(buf[p4+1])) {
					return start, p4 + 1, true
				}
			}
		}
		return start, p1, true
	}
}

func isUint(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// dictValueSpan finds the value span (as in readValueAt) belonging to the
// first occurrence of /key in dict, returning the span of the *key* token
// (keyStart) and the span of its value ([valStart,valEnd)).
func dictValueSpan(dict []byte, key string) (keyStart, keyEnd, valStart, valEnd int, ok bool) {
	needle := []byte("/" + key)
	idx := 0
	for {
		rel := bytes.Index(dict[idx:], needle)
		if rel < 0 {
			return 0, 0, 0, 0, false
		}
		pos := idx + rel
		after := pos + len(needle)
		if after < len(dict) && !isPDFDelim(dict[after]) && !isPDFWhitespace(dict[after]) {
			// Matched a longer key name, e.g. /Filter vs /FilterX; keep looking.
			idx = pos + 1
			continue
		}
		vs, ve, ok := readValueAt(dict, after)
		if !ok {
			return 0, 0, 0, 0, false
		}
		return pos, after, vs, ve, true
	}
}

// replaceDictValue replaces the value of /key in dict with newValue,
// leaving everything else untouched. If key is not present, dict is
// returned unchanged.
func replaceDictValue(dict []byte, key string, newValue []byte) []byte {
	_, keyEnd, valStart, valEnd, ok := dictValueSpan(dict, key)
	if !ok {
		return dict
	}
	out := make([]byte, 0, len(dict)-(valEnd-valStart)+len(newValue))
	out = append(out, dict[:keyEnd]...)
	out = append(out, ' ')
	out = append(out, newValue...)
	out = append(out, dict[valEnd:]...)
	return out
}

// removeDictKey removes /key and its value from dict entirely. If key is
// not present, dict is returned unchanged.
func removeDictKey(dict []byte, key string) []byte {
	keyStart, _, _, valEnd, ok := dictValueSpan(dict, key)
	if !ok {
		return dict
	}
	// Avoid leaving a double space behind when both neighbors are
	// whitespace: the byte before keyStart already provides a separator.
	if valEnd < len(dict) && isPDFWhitespace(dict[valEnd]) &&
		keyStart > 0 && isPDFWhitespace(dict[keyStart-1]) {
		valEnd++
	}
	out := make([]byte, 0, len(dict)-(valEnd-keyStart))
	out = append(out, dict[:keyStart]...)
	out = append(out, dict[valEnd:]...)
	return out
}
