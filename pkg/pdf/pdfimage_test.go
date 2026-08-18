package pdf

import (
	"bytes"
	"image"
	"image/jpeg"
	"strconv"
	"testing"
)

func TestDecodeRawImageGray(t *testing.T) {
	pix := []byte{0, 50, 100, 150, 200, 250} // 3x2
	img, ok := decodeRawImage(pix, 3, 2, 1)
	if !ok {
		t.Fatal("expected ok")
	}
	gray, isGray := img.(*image.Gray)
	if !isGray {
		t.Fatalf("expected *image.Gray, got %T", img)
	}
	if gray.GrayAt(2, 1).Y != 250 {
		t.Fatalf("pixel mismatch: %v", gray.GrayAt(2, 1))
	}
}

func TestDecodeRawImageRGB(t *testing.T) {
	pix := gradientRGB(4, 3)
	img, ok := decodeRawImage(pix, 4, 3, 3)
	if !ok {
		t.Fatal("expected ok")
	}
	r, g, b, _ := img.At(2, 1).RGBA()
	wantOff := (1*4 + 2) * 3
	if uint8(r>>8) != pix[wantOff] || uint8(g>>8) != pix[wantOff+1] || uint8(b>>8) != pix[wantOff+2] {
		t.Fatalf("pixel mismatch at (2,1): got (%d,%d,%d) want (%d,%d,%d)",
			r>>8, g>>8, b>>8, pix[wantOff], pix[wantOff+1], pix[wantOff+2])
	}
}

func TestDecodeRawImageTooShort(t *testing.T) {
	if _, ok := decodeRawImage([]byte{1, 2, 3}, 4, 3, 3); ok {
		t.Fatal("expected failure for truncated input")
	}
}

func TestExpandIndexed(t *testing.T) {
	palette := []byte{
		255, 0, 0,
		0, 255, 0,
		0, 0, 255,
	}
	indices := []byte{0, 1, 2, 1}
	out, ok := expandIndexed(indices, 2, 2, 3, palette)
	if !ok {
		t.Fatal("expected ok")
	}
	want := []byte{255, 0, 0, 0, 255, 0, 0, 0, 255, 0, 255, 0}
	if !bytes.Equal(out, want) {
		t.Fatalf("got %v want %v", out, want)
	}
}

func TestExpandIndexedOutOfRange(t *testing.T) {
	palette := []byte{255, 0, 0}
	indices := []byte{0, 5} // index 5 has no palette entry
	if _, ok := expandIndexed(indices, 2, 1, 3, palette); ok {
		t.Fatal("expected failure for out-of-range index")
	}
}

func TestDownsampleImageShrinksLongestSide(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4000, 2000))
	out := downsampleImage(img, 1000)
	b := out.Bounds()
	if b.Dx() != 1000 {
		t.Fatalf("expected width 1000, got %d", b.Dx())
	}
	if b.Dy() != 500 {
		t.Fatalf("expected height 500, got %d", b.Dy())
	}
}

func TestDownsampleImageNoopWhenSmaller(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 50))
	out := downsampleImage(img, 2000)
	if out.Bounds() != img.Bounds() {
		t.Fatalf("expected unchanged bounds, got %v", out.Bounds())
	}
}

// recompressAndReload runs RecompressImages on doc, serializes it, and
// re-parses the result, returning the fresh document for inspection.
func recompressAndReload(t *testing.T, pdfBytes []byte, quality, maxDim int) (RecompressStats, *Doc) {
	t.Helper()
	doc, err := Load(pdfBytes)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	stats := RecompressImages(doc, quality, maxDim)
	out := doc.Build()

	reloaded, err := Load(out)
	if err != nil {
		t.Fatalf("Load(rebuilt): %v", err)
	}
	return stats, reloaded
}

func TestRecompressJPEGImageShrinks(t *testing.T) {
	pdfBytes := buildJPEGImagePDF(t, 800, 600, 95)
	stats, reloaded := recompressAndReload(t, pdfBytes, 45, 0)

	if stats.ImagesFound != 1 || stats.Recompressed != 1 {
		t.Fatalf("stats: %+v", stats)
	}

	imgObj := reloaded.raw[4]
	dictPart := extractDictPart(imgObj)
	start, end, ok := streamSpan(imgObj, dictPart)
	if !ok {
		t.Fatal("could not locate recompressed stream")
	}
	if _, err := jpeg.Decode(bytes.NewReader(imgObj[start:end])); err != nil {
		t.Fatalf("recompressed stream is not valid JPEG: %v", err)
	}
}

func TestRecompressRawRGBImageBecomesSmallerAndValid(t *testing.T) {
	pdfBytes, original := buildRawRGBImagePDF(t, 400, 300, false)
	stats, reloaded := recompressAndReload(t, pdfBytes, 45, 0)

	if stats.ImagesFound != 1 || stats.Recompressed != 1 {
		t.Fatalf("stats: %+v", stats)
	}
	if len(reloaded.Build()) >= len(pdfBytes) {
		t.Fatalf("expected smaller output")
	}

	imgObj := reloaded.raw[4]
	dictPart := extractDictPart(imgObj)
	if !bytes.Contains(dictPart, []byte("/DCTDecode")) {
		t.Fatalf("expected /DCTDecode filter after conversion, dict: %s", dictPart)
	}
	if !bytes.Contains(dictPart, []byte("/DeviceRGB")) {
		t.Fatalf("expected /DeviceRGB colorspace, dict: %s", dictPart)
	}

	start, end, ok := streamSpan(imgObj, dictPart)
	if !ok {
		t.Fatal("could not locate stream")
	}
	img, err := jpeg.Decode(bytes.NewReader(imgObj[start:end]))
	if err != nil {
		t.Fatalf("not a valid JPEG: %v", err)
	}
	assertImageCloseToRGB(t, img, original, 400, 25)
}

func TestRecompressRawRGBImageWithPredictor(t *testing.T) {
	pdfBytes, original := buildRawRGBImagePDF(t, 500, 350, true)
	stats, reloaded := recompressAndReload(t, pdfBytes, 45, 0)

	if stats.ImagesFound != 1 || stats.Recompressed != 1 {
		t.Fatalf("stats: %+v", stats)
	}

	imgObj := reloaded.raw[4]
	dictPart := extractDictPart(imgObj)
	start, end, ok := streamSpan(imgObj, dictPart)
	if !ok {
		t.Fatal("could not locate stream")
	}
	img, err := jpeg.Decode(bytes.NewReader(imgObj[start:end]))
	if err != nil {
		t.Fatalf("not a valid JPEG: %v", err)
	}
	assertImageCloseToRGB(t, img, original, 500, 25)
}

func TestRecompressRawICCRGBImage(t *testing.T) {
	pdfBytes, original := buildRawICCRGBImagePDF(t, 300, 200)
	stats, reloaded := recompressAndReload(t, pdfBytes, 45, 0)

	if stats.ImagesFound != 1 || stats.Recompressed != 1 {
		t.Fatalf("stats: %+v", stats)
	}

	imgObj := reloaded.raw[4]
	dictPart := extractDictPart(imgObj)
	start, end, ok := streamSpan(imgObj, dictPart)
	if !ok {
		t.Fatal("could not locate stream")
	}
	img, err := jpeg.Decode(bytes.NewReader(imgObj[start:end]))
	if err != nil {
		t.Fatalf("not a valid JPEG: %v", err)
	}
	assertImageCloseToRGB(t, img, original, 300, 25)
}

func TestRecompressSkipsCMYK(t *testing.T) {
	// A raw CMYK bitmap must be left untouched (unsupported colorspace).
	pix := make([]byte, 10*10*4)
	var obj bytes.Buffer
	obj.WriteString("4 0 obj\n<< /Type /XObject /Subtype /Image /Width 10 /Height 10 " +
		"/ColorSpace /DeviceCMYK /BitsPerComponent 8 /Length ")
	obj.WriteString(strconv.Itoa(len(pix)))
	obj.WriteString(" >>\nstream\n")
	obj.Write(pix)
	obj.WriteString("\nendstream\nendobj\n")

	pdfBytes := buildTestPDF(t, obj.Bytes())
	stats, reloaded := recompressAndReload(t, pdfBytes, 45, 0)

	if stats.Recompressed != 0 {
		t.Fatalf("expected no recompression for CMYK, stats: %+v", stats)
	}
	if !bytes.Contains(reloaded.raw[4], []byte("/DeviceCMYK")) {
		t.Fatal("CMYK object should be unchanged")
	}
}

func TestIsCMYKImage(t *testing.T) {
	cmyk := image.NewCMYK(image.Rect(0, 0, 2, 2))
	if !isCMYKImage(cmyk) {
		t.Fatal("expected CMYK image to be detected")
	}
	rgba := image.NewRGBA(image.Rect(0, 0, 2, 2))
	if isCMYKImage(rgba) {
		t.Fatal("expected RGBA image not to be flagged as CMYK")
	}
}

func TestRecompressNeverGrowsObject(t *testing.T) {
	// Quality 100 on an already high-quality JPEG should not be replaced
	// if the recompressed version isn't smaller.
	pdfBytes := buildJPEGImagePDF(t, 200, 150, 100)
	stats, _ := recompressAndReload(t, pdfBytes, 100, 0)
	if stats.Recompressed != 0 {
		t.Fatalf("expected no beneficial recompression at quality 100, stats: %+v", stats)
	}
}

func assertImageCloseToRGB(t *testing.T, got image.Image, wantPix []byte, w, tolerance int) {
	t.Helper()
	b := got.Bounds()
	samplePoints := [][2]int{{0, 0}, {b.Dx() / 2, b.Dy() / 2}, {b.Dx() - 1, b.Dy() - 1}}
	for _, p := range samplePoints {
		x, y := p[0], p[1]
		r, g, bl, _ := got.At(x, y).RGBA()
		off := (y*w + x) * 3
		if off+2 >= len(wantPix) {
			continue
		}
		if diff(uint8(r>>8), wantPix[off]) > tolerance ||
			diff(uint8(g>>8), wantPix[off+1]) > tolerance ||
			diff(uint8(bl>>8), wantPix[off+2]) > tolerance {
			t.Fatalf("pixel (%d,%d) too different: got (%d,%d,%d) want ~(%d,%d,%d)",
				x, y, r>>8, g>>8, bl>>8, wantPix[off], wantPix[off+1], wantPix[off+2])
		}
	}
}

func diff(a, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}
