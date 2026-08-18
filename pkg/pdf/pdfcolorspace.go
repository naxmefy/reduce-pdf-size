package pdf

import (
	"bytes"
	"compress/zlib"
	"io"
	"regexp"
	"strconv"
)

var refRe = regexp.MustCompile(`^(\d+)\s+(\d+)\s+R$`)

// csInfo describes a resolved PDF color space in terms simple enough to
// build an image.Image from raw sample bytes.
type csInfo struct {
	kind     string // "gray", "rgb", or "" if unsupported
	ncomp    int
	indexed  bool
	baseKind string
	baseN    int
	palette  []byte // baseN bytes per index
	ok       bool
}

func namedColorSpace(name string) (kind string, ncomp int, ok bool) {
	switch name {
	case "/DeviceGray", "/CalGray", "/G":
		return "gray", 1, true
	case "/DeviceRGB", "/CalRGB", "/RGB":
		return "rgb", 3, true
	default:
		return "", 0, false
	}
}

// resolveRef parses a "N G R" token and returns the referenced object's raw
// bytes, if present in the document.
func resolveRef(doc *Doc, token []byte) ([]byte, bool) {
	m := refRe.FindSubmatch(bytes.TrimSpace(token))
	if m == nil {
		return nil, false
	}
	num, err := strconv.Atoi(string(m[1]))
	if err != nil {
		return nil, false
	}
	obj, ok := doc.raw[num]
	return obj, ok
}

// iccComponents extracts the /N (component count) from an ICCBased stream
// object's dictionary.
func iccComponents(obj []byte) (kind string, ncomp int, ok bool) {
	dictPart := extractDictPart(obj)
	_, _, vs, ve, found := dictValueSpan(dictPart, "N")
	if !found {
		return "", 0, false
	}
	n, err := strconv.Atoi(string(bytes.TrimSpace(dictPart[vs:ve])))
	if err != nil {
		return "", 0, false
	}
	switch n {
	case 1:
		return "gray", 1, true
	case 3:
		return "rgb", 3, true
	default:
		return "", 0, false
	}
}

// resolveColorSpace interprets a /ColorSpace value (as raw text, exactly as
// it appears in a dictionary: a name, an indirect reference, or an inline
// array) and reports whether it is one this tool knows how to convert to
// JPEG. Only 8-bit DeviceGray/DeviceRGB (directly, via ICCBased, or via a
// CalGray/CalRGB/Indexed wrapper) are supported; anything else (CMYK,
// Separation, DeviceN, Lab, ...) is reported as unsupported so the caller
// can safely leave the image untouched.
func resolveColorSpace(doc *Doc, value []byte) csInfo {
	value = bytes.TrimSpace(value)
	if len(value) == 0 {
		return csInfo{}
	}

	if value[0] == '/' {
		if kind, n, ok := namedColorSpace(string(value)); ok {
			return csInfo{kind: kind, ncomp: n, ok: true}
		}
		return csInfo{}
	}

	if refRe.Match(value) {
		obj, ok := resolveRef(doc, value)
		if !ok {
			return csInfo{}
		}
		return resolveColorSpaceObject(doc, obj)
	}

	if value[0] == '[' {
		return resolveColorSpaceArray(doc, value)
	}

	return csInfo{}
}

// resolveColorSpaceObject resolves a color space stored as a standalone
// indirect object (its body is either an array like [/ICCBased 5 0 R], or
// (for ICCBased profile streams referenced directly) a stream object that
// itself carries /N).
func resolveColorSpaceObject(doc *Doc, obj []byte) csInfo {
	dictPart := extractDictPart(obj)
	trimmed := bytes.TrimSpace(dictPart)
	// Strip "N G obj" header if this is a plain (non-stream) object whose
	// body is just an array value.
	if _, afterHeader, found := bytes.Cut(trimmed, []byte("obj")); found {
		afterHeader = bytes.TrimSpace(afterHeader)
		if len(afterHeader) > 0 && afterHeader[0] == '[' {
			return resolveColorSpaceArray(doc, afterHeader)
		}
	}
	if kind, n, ok := iccComponents(obj); ok {
		return csInfo{kind: kind, ncomp: n, ok: true}
	}
	return csInfo{}
}

func resolveColorSpaceArray(doc *Doc, arr []byte) csInfo {
	start, end, ok := readValueAt(arr, 0)
	if !ok || arr[start] != '[' {
		return csInfo{}
	}
	inner := arr[start+1 : end-1]

	fs, fe, ok := readValueAt(inner, 0)
	if !ok {
		return csInfo{}
	}
	family := string(inner[fs:fe])

	switch family {
	case "/CalRGB":
		return csInfo{kind: "rgb", ncomp: 3, ok: true}
	case "/CalGray":
		return csInfo{kind: "gray", ncomp: 1, ok: true}
	case "/ICCBased":
		vs, ve, ok := readValueAt(inner, fe)
		if !ok {
			return csInfo{}
		}
		obj, ok := resolveRef(doc, inner[vs:ve])
		if !ok {
			return csInfo{}
		}
		if kind, n, ok := iccComponents(obj); ok {
			return csInfo{kind: kind, ncomp: n, ok: true}
		}
		return csInfo{}
	case "/Indexed":
		return resolveIndexed(doc, inner, fe)
	default:
		return csInfo{}
	}
}

func resolveIndexed(doc *Doc, inner []byte, pos int) csInfo {
	baseStart, baseEnd, ok := readValueAt(inner, pos)
	if !ok {
		return csInfo{}
	}
	base := resolveColorSpace(doc, inner[baseStart:baseEnd])
	if !base.ok || base.indexed {
		return csInfo{}
	}

	hivalStart, hivalEnd, ok := readValueAt(inner, baseEnd)
	if !ok {
		return csInfo{}
	}
	_ = hivalStart
	_ = hivalEnd

	lookupStart, lookupEnd, ok := readValueAt(inner, hivalEnd)
	if !ok {
		return csInfo{}
	}
	lookupToken := inner[lookupStart:lookupEnd]

	var palette []byte
	if refRe.Match(bytes.TrimSpace(lookupToken)) {
		obj, ok := resolveRef(doc, lookupToken)
		if !ok {
			return csInfo{}
		}
		dictPart := extractDictPart(obj)
		sStart, sEnd, ok := streamSpan(obj, dictPart)
		if !ok {
			return csInfo{}
		}
		raw := obj[sStart:sEnd]
		if bytes.Contains(dictPart, []byte("/FlateDecode")) {
			zr, err := zlib.NewReader(bytes.NewReader(raw))
			if err != nil {
				return csInfo{}
			}
			decompressed, err := io.ReadAll(zr)
			if err != nil {
				return csInfo{}
			}
			palette = decompressed
		} else if !bytes.Contains(dictPart, []byte("/Filter")) {
			palette = raw
		} else {
			return csInfo{}
		}
	} else if len(lookupToken) > 0 && lookupToken[0] == '(' {
		palette = decodePDFStringLiteral(lookupToken)
	} else {
		return csInfo{}
	}

	return csInfo{
		kind:     "gray",
		indexed:  true,
		baseKind: base.kind,
		baseN:    base.ncomp,
		palette:  palette,
		ok:       true,
	}
}

// decodePDFStringLiteral decodes a "(...)" PDF string literal's bytes,
// handling backslash escapes for parens, backslash itself, the common
// control-character escapes, and octal byte escapes (\ddd) as used for
// binary palette data in Indexed color spaces.
func decodePDFStringLiteral(tok []byte) []byte {
	if len(tok) < 2 || tok[0] != '(' || tok[len(tok)-1] != ')' {
		return nil
	}
	inner := tok[1 : len(tok)-1]
	out := make([]byte, 0, len(inner))
	for i := 0; i < len(inner); i++ {
		if inner[i] != '\\' || i+1 >= len(inner) {
			out = append(out, inner[i])
			continue
		}
		i++
		switch inner[i] {
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case '0', '1', '2', '3', '4', '5', '6', '7':
			v := int(inner[i] - '0')
			for k := 0; k < 2 && i+1 < len(inner) && inner[i+1] >= '0' && inner[i+1] <= '7'; k++ {
				i++
				v = v*8 + int(inner[i]-'0')
			}
			out = append(out, byte(v))
		default:
			out = append(out, inner[i])
		}
	}
	return out
}
