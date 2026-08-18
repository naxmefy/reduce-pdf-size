package pdf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"regexp"
	"sort"
	"strconv"
)

var (
	imageSubtypeRe   = regexp.MustCompile(`/Subtype\s*/Image\b`)
	dctFilterRe      = regexp.MustCompile(`/Filter\s*/DCTDecode\b`)
	dctFilterArrRe   = regexp.MustCompile(`/Filter\s*\[\s*/DCTDecode\s*\]`)
	flateFilterRe    = regexp.MustCompile(`/Filter\s*/FlateDecode\b`)
	flateFilterArrRe = regexp.MustCompile(`/Filter\s*\[\s*/FlateDecode\s*\]`)
	filterKeyRe      = regexp.MustCompile(`/Filter\b`)
	imageMaskRe      = regexp.MustCompile(`/ImageMask\s+true\b`)
	widthRe          = regexp.MustCompile(`/Width\s+(\d+)`)
	heightRe         = regexp.MustCompile(`/Height\s+(\d+)`)
	bpcRe            = regexp.MustCompile(`/BitsPerComponent\s+(\d+)`)
)

type recompressStats struct {
	ImagesFound  int
	Recompressed int
	BytesSaved   int64
	Notes        []string
}

func (s *recompressStats) note(format string, args ...any) {
	s.Notes = append(s.Notes, fmt.Sprintf(format, args...))
}

// RecompressImages walks every object in the document and re-encodes
// embedded images as lower-quality JPEGs, optionally downsampling large
// images first. An object is only rewritten if the result is smaller than
// the original stream. Two source encodings are handled: already-JPEG
// (DCTDecode) streams are simply recompressed, and uncompressed/Flate raw
// bitmaps (DeviceGray/DeviceRGB, directly or via ICCBased/Indexed) are
// converted to JPEG.
func RecompressImages(doc *pdfDoc, quality int, maxDim int) recompressStats {
	var stats recompressStats

	nums := make([]int, 0, len(doc.raw))
	for n := range doc.raw {
		nums = append(nums, n)
	}
	sort.Ints(nums)

	for _, num := range nums {
		if doc.skip[num] {
			continue
		}
		raw := doc.raw[num]
		dictPart := extractDictPart(raw)
		if !imageSubtypeRe.Match(dictPart) {
			continue
		}
		if imageMaskRe.Match(dictPart) {
			continue
		}

		switch {
		case dctFilterRe.Match(dictPart) || dctFilterArrRe.Match(dictPart):
			processJPEGObject(doc, num, quality, maxDim, &stats)
		case flateFilterRe.Match(dictPart) || flateFilterArrRe.Match(dictPart) || !filterKeyRe.Match(dictPart):
			processRawBitmapObject(doc, num, quality, maxDim, &stats)
		default:
			stats.note("obj %d: skipped (unsupported filter)", num)
		}
	}

	return stats
}

func finalizeImage(doc *pdfDoc, num int, dictPart []byte, img image.Image, origLen int, quality, maxDim int, colorSpaceOverride string, stats *recompressStats) {
	final := img
	b := img.Bounds()
	if maxDim > 0 && (b.Dx() > maxDim || b.Dy() > maxDim) {
		final = downsampleImage(img, maxDim)
	}

	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, final, &jpeg.Options{Quality: quality}); err != nil {
		stats.note("obj %d: skipped (JPEG encode failed: %v)", num, err)
		return
	}
	newBytes := encoded.Bytes()
	if len(newBytes) >= origLen {
		stats.note("obj %d: skipped (no size reduction achieved)", num)
		return
	}

	newDict := replaceDictValue(dictPart, "Filter", []byte("/DCTDecode"))
	newDict = removeDictKey(newDict, "DecodeParms")
	newDict = removeDictKey(newDict, "DP")
	if colorSpaceOverride != "" {
		newDict = replaceDictValue(newDict, "ColorSpace", []byte(colorSpaceOverride))
	}
	newDict = replaceDictValue(newDict, "BitsPerComponent", []byte("8"))
	newDict = replaceDictValue(newDict, "Length", []byte(strconv.Itoa(len(newBytes))))

	var nb bytes.Buffer
	nb.Write(newDict)
	nb.WriteString("stream\n")
	nb.Write(newBytes)
	nb.WriteString("\nendstream\nendobj\n")

	doc.raw[num] = nb.Bytes()
	stats.Recompressed++
	stats.BytesSaved += int64(origLen - len(newBytes))
	stats.note("obj %d: recompressed (%d -> %d bytes)", num, origLen, len(newBytes))
}

func processJPEGObject(doc *pdfDoc, num int, quality, maxDim int, stats *recompressStats) {
	raw := doc.raw[num]
	dictPart := extractDictPart(raw)

	start, end, ok := streamSpan(raw, dictPart)
	if !ok || start >= end || end > len(raw) {
		stats.note("obj %d: skipped (stream bounds unclear)", num)
		return
	}
	imgBytes := raw[start:end]

	img, err := jpeg.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		stats.note("obj %d: skipped (JPEG decode failed: %v)", num, err)
		return
	}
	if isCMYKImage(img) {
		// Re-encoding would silently convert CMYK to RGB while the object's
		// /ColorSpace still claims DeviceCMYK, corrupting the colors.
		stats.note("obj %d: skipped (CMYK JPEG not supported)", num)
		return
	}
	stats.ImagesFound++
	finalizeImage(doc, num, dictPart, img, len(imgBytes), quality, maxDim, "", stats)
}

func isCMYKImage(img image.Image) bool {
	_, ok := img.(*image.CMYK)
	return ok
}

func processRawBitmapObject(doc *pdfDoc, num int, quality, maxDim int, stats *recompressStats) {
	raw := doc.raw[num]
	dictPart := extractDictPart(raw)

	wm := widthRe.FindSubmatch(dictPart)
	hm := heightRe.FindSubmatch(dictPart)
	bm := bpcRe.FindSubmatch(dictPart)
	if wm == nil || hm == nil {
		return
	}
	width, _ := strconv.Atoi(string(wm[1]))
	height, _ := strconv.Atoi(string(hm[1]))
	bpc := 8
	if bm != nil {
		bpc, _ = strconv.Atoi(string(bm[1]))
	}
	if bpc != 8 || width <= 0 || height <= 0 {
		return
	}

	_, _, csStart, csEnd, ok := dictValueSpan(dictPart, "ColorSpace")
	if !ok {
		return
	}
	cs := resolveColorSpace(doc, dictPart[csStart:csEnd])
	if !cs.ok {
		return
	}
	if cs.indexed {
		if cs.baseKind != "gray" && cs.baseKind != "rgb" {
			return
		}
	} else if cs.kind != "gray" && cs.kind != "rgb" {
		return
	}

	isFlate := flateFilterRe.Match(dictPart) || flateFilterArrRe.Match(dictPart)

	start, end, ok := streamSpan(raw, dictPart)
	if !ok || start >= end || end > len(raw) {
		return
	}
	streamBytes := raw[start:end]
	origLen := len(streamBytes)

	var data []byte
	if isFlate {
		zr, err := zlib.NewReader(bytes.NewReader(streamBytes))
		if err != nil {
			stats.note("obj %d: skipped (flate decode failed: %v)", num, err)
			return
		}
		decompressed, err := io.ReadAll(zr)
		if err != nil {
			stats.note("obj %d: skipped (flate decode failed: %v)", num, err)
			return
		}
		data = decompressed
	} else {
		data = streamBytes
	}

	sampleComponents := cs.ncomp
	if cs.indexed {
		sampleComponents = 1
	}
	predictor, columns := parsePredictorParams(dictPart, width)

	data, ok = reversePredictor(data, predictor, sampleComponents, 8, columns)
	if !ok {
		stats.note("obj %d: skipped (predictor not supported)", num)
		return
	}

	ncompFinal := cs.ncomp
	kindFinal := cs.kind
	if cs.indexed {
		expanded, ok := expandIndexed(data, width, height, cs.baseN, cs.palette)
		if !ok {
			stats.note("obj %d: skipped (invalid indexed palette)", num)
			return
		}
		data = expanded
		ncompFinal = cs.baseN
		kindFinal = cs.baseKind
	}

	img, ok := decodeRawImage(data, width, height, ncompFinal)
	if !ok {
		stats.note("obj %d: skipped (incomplete image data)", num)
		return
	}
	stats.ImagesFound++

	colorSpaceName := "/DeviceRGB"
	if kindFinal == "gray" {
		colorSpaceName = "/DeviceGray"
	}
	finalizeImage(doc, num, dictPart, img, origLen, quality, maxDim, colorSpaceName, stats)
}

func parsePredictorParams(dictPart []byte, width int) (predictor, columns int) {
	predictor = 1
	columns = width

	_, _, dpStart, dpEnd, ok := dictValueSpan(dictPart, "DecodeParms")
	if !ok {
		_, _, dpStart, dpEnd, ok = dictValueSpan(dictPart, "DP")
	}
	if !ok {
		return predictor, columns
	}
	dp := dictPart[dpStart:dpEnd]
	if len(dp) == 0 || dp[0] != '<' {
		return predictor, columns
	}

	if _, _, vs, ve, ok := dictValueSpan(dp, "Predictor"); ok {
		if p, err := strconv.Atoi(string(bytes.TrimSpace(dp[vs:ve]))); err == nil {
			predictor = p
		}
	}
	if _, _, vs, ve, ok := dictValueSpan(dp, "Columns"); ok {
		if c, err := strconv.Atoi(string(bytes.TrimSpace(dp[vs:ve]))); err == nil {
			columns = c
		}
	}
	return predictor, columns
}

// decodeRawImage builds an image.Image from packed 8-bit samples (row-major,
// no padding), supporting 1 (gray) or 3 (RGB) components per pixel.
func decodeRawImage(pix []byte, w, h, ncomp int) (image.Image, bool) {
	rowBytes := w * ncomp
	if rowBytes <= 0 || len(pix) < rowBytes*h {
		return nil, false
	}

	switch ncomp {
	case 1:
		img := image.NewGray(image.Rect(0, 0, w, h))
		for y := range h {
			copy(img.Pix[y*img.Stride:y*img.Stride+w], pix[y*rowBytes:y*rowBytes+w])
		}
		return img, true
	case 3:
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := range h {
			srcRow := pix[y*rowBytes : (y+1)*rowBytes]
			dstRow := img.Pix[y*img.Stride : y*img.Stride+w*4]
			for x := range w {
				dstRow[x*4+0] = srcRow[x*3+0]
				dstRow[x*4+1] = srcRow[x*3+1]
				dstRow[x*4+2] = srcRow[x*3+2]
				dstRow[x*4+3] = 255
			}
		}
		return img, true
	default:
		return nil, false
	}
}

// expandIndexed maps 8-bit palette indices to baseN-component samples.
func expandIndexed(indices []byte, w, h, baseN int, palette []byte) ([]byte, bool) {
	count := w * h
	if len(indices) < count || baseN <= 0 {
		return nil, false
	}
	out := make([]byte, count*baseN)
	for i := range count {
		off := int(indices[i]) * baseN
		if off+baseN > len(palette) {
			return nil, false
		}
		copy(out[i*baseN:i*baseN+baseN], palette[off:off+baseN])
	}
	return out, true
}

// downsampleImage shrinks img so its longest side is at most maxDim,
// using simple box averaging. Only called when the image actually
// exceeds maxDim.
func downsampleImage(img image.Image, maxDim int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 || (w <= maxDim && h <= maxDim) {
		return img
	}

	longest := max(w, h)
	scale := float64(maxDim) / float64(longest)
	nw := int(float64(w) * scale)
	nh := int(float64(h) * scale)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}

	out := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy0 := y * h / nh
		sy1 := (y + 1) * h / nh
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for x := 0; x < nw; x++ {
			sx0 := x * w / nw
			sx1 := (x + 1) * w / nw
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}

			var rSum, gSum, bSum, count uint32
			for sy := sy0; sy < sy1 && sy < h; sy++ {
				for sx := sx0; sx < sx1 && sx < w; sx++ {
					c := color.RGBAModel.Convert(img.At(b.Min.X+sx, b.Min.Y+sy)).(color.RGBA)
					rSum += uint32(c.R)
					gSum += uint32(c.G)
					bSum += uint32(c.B)
					count++
				}
			}
			if count == 0 {
				count = 1
			}
			out.Set(x, y, color.RGBA{
				R: uint8(rSum / count),
				G: uint8(gSum / count),
				B: uint8(bSum / count),
				A: 255,
			})
		}
	}
	return out
}
