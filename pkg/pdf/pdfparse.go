package pdf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
)

var (
	objHeaderRe   = regexp.MustCompile(`\b(\d+)[ \t]+(\d+)[ \t]+obj\b`)
	streamKwRe    = regexp.MustCompile(`stream\r?\n`)
	endstreamRe   = regexp.MustCompile(`\r?\n?endstream`)
	directLenRe   = regexp.MustCompile(`/Length\s+(\d+)\b`)
	indirectLenRe = regexp.MustCompile(`/Length\s+\d+\s+\d+\s+R\b`)
	objStmRe      = regexp.MustCompile(`/Type\s*/ObjStm\b`)
	xrefStmRe     = regexp.MustCompile(`/Type\s*/XRef\b`)
	nRe           = regexp.MustCompile(`/N\s+(\d+)`)
	firstRe       = regexp.MustCompile(`/First\s+(\d+)`)
	rootRe        = regexp.MustCompile(`/Root\s+(\d+)\s+(\d+)\s+R`)
	infoRe        = regexp.MustCompile(`/Info\s+(\d+)\s+(\d+)\s+R`)
)

// pdfDoc holds the flat object model of a PDF, keyed by object number.
type pdfDoc struct {
	raw  map[int][]byte // full "N G obj ... endobj" bytes per object number
	gen  map[int]int    // generation number per object number
	skip map[int]bool   // objects excluded from output (ObjStm/XRef containers)

	rootNum, rootGen int
	infoNum, infoGen int
	hasInfo          bool

	header []byte
}

func isEncrypted(buf []byte) bool {
	return bytes.Contains(buf, []byte("/Encrypt"))
}

func findHeader(buf []byte) ([]byte, error) {
	idx := bytes.Index(buf[:min(1024, len(buf))], []byte("%PDF-"))
	if idx < 0 {
		return nil, fmt.Errorf("not a valid PDF file (missing %%PDF- header)")
	}
	nl := bytes.IndexByte(buf[idx:], '\n')
	if nl < 0 {
		return nil, fmt.Errorf("could not read PDF header")
	}
	return buf[idx : idx+nl+1], nil
}

// extractDictPart returns everything up to (and including, harmlessly) the
// object header and dictionary, stopping right before the "stream" keyword.
// For objects without a stream this is simply the whole raw object.
func extractDictPart(raw []byte) []byte {
	loc := streamKwRe.FindIndex(raw)
	if loc == nil {
		return raw
	}
	return raw[:loc[0]]
}

// streamSpan locates the byte range of stream data within raw, using a
// direct /Length integer when available and falling back to a textual
// search for "endstream" otherwise.
func streamSpan(raw []byte, dictPart []byte) (start, end int, ok bool) {
	loc := streamKwRe.FindIndex(raw)
	if loc == nil {
		return 0, 0, false
	}
	start = loc[1]

	if !indirectLenRe.Match(dictPart) {
		if m := directLenRe.FindSubmatch(dictPart); m != nil {
			if n, err := strconv.Atoi(string(m[1])); err == nil {
				candidateEnd := start + n
				if candidateEnd <= len(raw) {
					return start, candidateEnd, true
				}
			}
		}
	}

	rel := endstreamRe.FindIndex(raw[start:])
	if rel == nil {
		return 0, 0, false
	}
	end = start + rel[0]
	return start, end, true
}

// scanObjects performs a brute-force scan for top-level "N G obj ... endobj"
// spans. Later occurrences of the same object number (incremental updates)
// override earlier ones.
func scanObjects(buf []byte) (map[int][]byte, map[int]int) {
	raw := map[int][]byte{}
	gen := map[int]int{}

	matches := objHeaderRe.FindAllSubmatchIndex(buf, -1)
	for i, m := range matches {
		numStr := string(buf[m[2]:m[3]])
		genStr := string(buf[m[4]:m[5]])
		num, err := strconv.Atoi(numStr)
		if err != nil {
			continue
		}
		g, err := strconv.Atoi(genStr)
		if err != nil {
			continue
		}

		searchFrom := m[1]
		searchTo := len(buf)
		if i+1 < len(matches) {
			searchTo = matches[i+1][0]
		}
		region := buf[searchFrom:searchTo]
		idx := bytes.LastIndex(region, []byte("endobj"))
		if idx == -1 {
			continue
		}
		end := searchFrom + idx + len("endobj")

		raw[num] = buf[m[0]:end]
		gen[num] = g
	}
	return raw, gen
}

// expandObjectStreams decompresses any /Type/ObjStm objects and adds their
// contained objects to rawObjects (loose/incremental definitions already
// present take precedence). Container objects (ObjStm and XRef streams) are
// marked for exclusion from the rewritten output.
func expandObjectStreams(raw map[int][]byte, gen map[int]int) map[int]bool {
	skip := map[int]bool{}

	nums := make([]int, 0, len(raw))
	for n := range raw {
		nums = append(nums, n)
	}
	sort.Ints(nums)

	for _, num := range nums {
		obj := raw[num]
		dictPart := extractDictPart(obj)

		if xrefStmRe.Match(dictPart) {
			skip[num] = true
			continue
		}
		if !objStmRe.Match(dictPart) {
			continue
		}

		nm := nRe.FindSubmatch(dictPart)
		fm := firstRe.FindSubmatch(dictPart)
		if nm == nil || fm == nil {
			continue
		}
		count, err1 := strconv.Atoi(string(nm[1]))
		first, err2 := strconv.Atoi(string(fm[1]))
		if err1 != nil || err2 != nil {
			continue
		}

		start, end, ok := streamSpan(obj, dictPart)
		if !ok {
			continue
		}
		streamBytes := obj[start:end]

		zr, err := zlib.NewReader(bytes.NewReader(streamBytes))
		if err != nil {
			skip[num] = true
			continue
		}
		decompressed, err := io.ReadAll(zr)
		if err != nil || first > len(decompressed) {
			skip[num] = true
			continue
		}

		fields := bytes.Fields(decompressed[:first])
		if len(fields) < count*2 {
			skip[num] = true
			continue
		}

		type pair struct{ num, off int }
		pairs := make([]pair, count)
		for i := range count {
			onum, e1 := strconv.Atoi(string(fields[i*2]))
			ooff, e2 := strconv.Atoi(string(fields[i*2+1]))
			if e1 != nil || e2 != nil {
				pairs = nil
				break
			}
			pairs[i] = pair{onum, ooff}
		}

		for i, p := range pairs {
			objStart := first + p.off
			objEnd := len(decompressed)
			if i+1 < len(pairs) {
				objEnd = first + pairs[i+1].off
			}
			if objStart < 0 || objEnd > len(decompressed) || objStart > objEnd {
				continue
			}
			if _, exists := raw[p.num]; exists {
				continue
			}
			val := bytes.TrimSpace(decompressed[objStart:objEnd])
			var nb bytes.Buffer
			fmt.Fprintf(&nb, "%d 0 obj\n", p.num)
			nb.Write(val)
			nb.WriteString("\nendobj\n")
			raw[p.num] = nb.Bytes()
			gen[p.num] = 0
		}

		skip[num] = true
	}

	return skip
}

func LoadPDF(buf []byte) (*pdfDoc, error) {
	if isEncrypted(buf) {
		return nil, fmt.Errorf("encrypted PDFs are not supported")
	}

	header, err := findHeader(buf)
	if err != nil {
		return nil, err
	}

	raw, gen := scanObjects(buf)
	if len(raw) == 0 {
		return nil, fmt.Errorf("no PDF objects could be found")
	}
	skip := expandObjectStreams(raw, gen)

	rootMatches := rootRe.FindAllSubmatch(buf, -1)
	if len(rootMatches) == 0 {
		return nil, fmt.Errorf("root object (catalog) could not be found")
	}
	last := rootMatches[len(rootMatches)-1]
	rootNum, _ := strconv.Atoi(string(last[1]))
	rootGen, _ := strconv.Atoi(string(last[2]))

	doc := &pdfDoc{
		raw:     raw,
		gen:     gen,
		skip:    skip,
		rootNum: rootNum,
		rootGen: rootGen,
		header:  header,
	}

	if infoMatches := infoRe.FindAllSubmatch(buf, -1); len(infoMatches) > 0 {
		lastInfo := infoMatches[len(infoMatches)-1]
		infoNum, _ := strconv.Atoi(string(lastInfo[1]))
		infoGen, _ := strconv.Atoi(string(lastInfo[2]))
		if _, exists := raw[infoNum]; exists {
			doc.infoNum = infoNum
			doc.infoGen = infoGen
			doc.hasInfo = true
		}
	}

	return doc, nil
}

// Build serializes the document into a fresh, classic-xref PDF.
func (doc *pdfDoc) Build() []byte {
	nums := make([]int, 0, len(doc.raw))
	maxNum := 0
	for n := range doc.raw {
		nums = append(nums, n)
		if n > maxNum {
			maxNum = n
		}
	}
	sort.Ints(nums)

	var out bytes.Buffer
	out.Write(doc.header)

	offsets := make(map[int]int64, len(nums))
	for _, n := range nums {
		if doc.skip[n] {
			continue
		}
		offsets[n] = int64(out.Len())
		objBytes := doc.raw[n]
		out.Write(objBytes)
		if len(objBytes) == 0 || objBytes[len(objBytes)-1] != '\n' {
			out.WriteByte('\n')
		}
	}

	xrefOffset := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n", maxNum+1)
	out.WriteString("0000000000 65535 f \n")
	for n := 1; n <= maxNum; n++ {
		if off, ok := offsets[n]; ok {
			fmt.Fprintf(&out, "%010d %05d n \n", off, doc.gen[n])
		} else {
			out.WriteString("0000000000 00000 f \n")
		}
	}

	out.WriteString("trailer\n")
	if doc.hasInfo {
		fmt.Fprintf(&out, "<< /Size %d /Root %d %d R /Info %d %d R >>\n",
			maxNum+1, doc.rootNum, doc.rootGen, doc.infoNum, doc.infoGen)
	} else {
		fmt.Fprintf(&out, "<< /Size %d /Root %d %d R >>\n", maxNum+1, doc.rootNum, doc.rootGen)
	}
	fmt.Fprintf(&out, "startxref\n%d\n%%%%EOF\n", xrefOffset)

	return out.Bytes()
}
