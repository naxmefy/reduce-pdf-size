package pdf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"maps"
	"math"
	"sort"
	"testing"
)

// buildTestPDF assembles a minimal one-page classic-xref PDF from the given
// extra objects (numbered starting at 4; 1-3 are Catalog/Pages/Page) plus a
// content stream that paints image object 4 onto the page.
func buildTestPDF(t *testing.T, imageObj []byte) []byte {
	t.Helper()

	content := []byte("q 600 0 0 400 6 6 cm /Im1 Do Q")

	objs := map[int][]byte{
		1: []byte("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n"),
		2: []byte("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n"),
		3: []byte("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] " +
			"/Resources << /XObject << /Im1 4 0 R >> >> /Contents 5 0 R >>\nendobj\n"),
		4: imageObj,
	}
	var contentObj bytes.Buffer
	fmt.Fprintf(&contentObj, "5 0 obj\n<< /Length %d >>\nstream\n", len(content))
	contentObj.Write(content)
	contentObj.WriteString("\nendstream\nendobj\n")
	objs[5] = contentObj.Bytes()

	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make(map[int]int)
	for i := 1; i <= 5; i++ {
		offsets[i] = out.Len()
		out.Write(objs[i])
	}
	xrefOff := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n", 6)
	out.WriteString("0000000000 65535 f \n")
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&out, "%010d %05d n \n", offsets[i], 0)
	}
	out.WriteString("trailer\n")
	fmt.Fprintf(&out, "<< /Size %d /Root 1 0 R >>\n", 6)
	fmt.Fprintf(&out, "startxref\n%d\n%%%%EOF\n", xrefOff)
	return out.Bytes()
}

// gradientRGB generates a photo-like (high-frequency, non-linear) test
// pattern. A plain linear ramp would let a PNG "Up" predictor reduce each
// row to a near-constant delta, which Flate then compresses far better
// than any real photo ever would -- masking the very inefficiency that
// JPEG recompression is meant to fix. Sinusoids avoid that pathology.
func gradientRGB(w, h int) []byte {
	pix := make([]byte, w*h*3)
	for y := range h {
		for x := range w {
			off := (y*w + x) * 3
			r := 128 + 127*math.Sin(float64(x)/9.7)
			g := 128 + 127*math.Cos(float64(y)/13.3)
			b := 128 + 127*math.Sin(float64(x-y)/17.1)
			pix[off] = byte(r)
			pix[off+1] = byte(g)
			pix[off+2] = byte(b)
		}
	}
	return pix
}

func makeJPEGBytes(t *testing.T, w, h, quality int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	pix := gradientRGB(w, h)
	for y := range h {
		for x := range w {
			off := (y*w + x) * 3
			img.Set(x, y, color.RGBA{pix[off], pix[off+1], pix[off+2], 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}
	return buf.Bytes()
}

// buildJPEGImagePDF builds a full test PDF containing one DCTDecode image.
func buildJPEGImagePDF(t *testing.T, w, h, quality int) []byte {
	t.Helper()
	jpegBytes := makeJPEGBytes(t, w, h, quality)
	var obj bytes.Buffer
	fmt.Fprintf(&obj, "4 0 obj\n<< /Type /XObject /Subtype /Image /Width %d /Height %d "+
		"/ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n",
		w, h, len(jpegBytes))
	obj.Write(jpegBytes)
	obj.WriteString("\nendstream\nendobj\n")
	return buildTestPDF(t, obj.Bytes())
}

func applyPNGPredictor(pix []byte, rowBytes int) []byte {
	numRows := len(pix) / rowBytes
	out := make([]byte, 0, (rowBytes+1)*numRows)
	prev := make([]byte, rowBytes)
	for r := range numRows {
		row := pix[r*rowBytes : (r+1)*rowBytes]
		filtered := make([]byte, rowBytes)
		for i := range row {
			filtered[i] = row[i] - prev[i]
		}
		out = append(out, 2) // "Up" filter type
		out = append(out, filtered...)
		prev = row
	}
	return out
}

// buildRawRGBImagePDF builds a full test PDF containing one FlateDecode
// DeviceRGB raw bitmap image, optionally PNG-predictor filtered.
func buildRawRGBImagePDF(t *testing.T, w, h int, usePredictor bool) ([]byte, []byte) {
	t.Helper()
	pix := gradientRGB(w, h)

	toCompress := pix
	decodeParms := ""
	if usePredictor {
		toCompress = applyPNGPredictor(pix, w*3)
		decodeParms = fmt.Sprintf(" /DecodeParms << /Predictor 15 /Colors 3 /BitsPerComponent 8 /Columns %d >>", w)
	}

	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	zw.Write(toCompress)
	zw.Close()

	var obj bytes.Buffer
	fmt.Fprintf(&obj, "4 0 obj\n<< /Type /XObject /Subtype /Image /Width %d /Height %d "+
		"/ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode%s /Length %d >>\nstream\n",
		w, h, decodeParms, compressed.Len())
	obj.Write(compressed.Bytes())
	obj.WriteString("\nendstream\nendobj\n")
	return buildTestPDF(t, obj.Bytes()), pix
}

// buildRawICCRGBImagePDF is like buildRawRGBImagePDF but the color space is
// an indirect reference to an ICCBased stream object (component count 3),
// matching what most real-world PDF writers emit for "DeviceRGB".
func buildRawICCRGBImagePDF(t *testing.T, w, h int) ([]byte, []byte) {
	t.Helper()
	pix := gradientRGB(w, h)

	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	zw.Write(pix)
	zw.Close()

	iccProfile := bytes.Repeat([]byte{0}, 64)
	var iccCompressed bytes.Buffer
	zw2 := zlib.NewWriter(&iccCompressed)
	zw2.Write(iccProfile)
	zw2.Close()

	var iccObj bytes.Buffer
	fmt.Fprintf(&iccObj, "6 0 obj\n<< /N 3 /Filter /FlateDecode /Length %d >>\nstream\n", iccCompressed.Len())
	iccObj.Write(iccCompressed.Bytes())
	iccObj.WriteString("\nendstream\nendobj\n")

	var csObj bytes.Buffer
	fmt.Fprintf(&csObj, "7 0 obj\n[/ICCBased 6 0 R]\nendobj\n")

	var imgObj bytes.Buffer
	fmt.Fprintf(&imgObj, "4 0 obj\n<< /Type /XObject /Subtype /Image /Width %d /Height %d "+
		"/ColorSpace 7 0 R /BitsPerComponent 8 /Filter /FlateDecode /Length %d >>\nstream\n",
		w, h, compressed.Len())
	imgObj.Write(compressed.Bytes())
	imgObj.WriteString("\nendstream\nendobj\n")

	base := buildTestPDFWithExtras(t, imgObj.Bytes(), map[int][]byte{
		6: iccObj.Bytes(),
		7: csObj.Bytes(),
	})
	return base, pix
}

// buildTestPDFWithExtras is like buildTestPDF but allows adding extra
// numbered objects (e.g. color space helper objects).
func buildTestPDFWithExtras(t *testing.T, imageObj []byte, extras map[int][]byte) []byte {
	t.Helper()

	content := []byte("q 600 0 0 400 6 6 cm /Im1 Do Q")

	objs := map[int][]byte{
		1: []byte("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n"),
		2: []byte("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n"),
		3: []byte("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] " +
			"/Resources << /XObject << /Im1 4 0 R >> >> /Contents 5 0 R >>\nendobj\n"),
		4: imageObj,
	}
	var contentObj bytes.Buffer
	fmt.Fprintf(&contentObj, "5 0 obj\n<< /Length %d >>\nstream\n", len(content))
	contentObj.Write(content)
	contentObj.WriteString("\nendstream\nendobj\n")
	objs[5] = contentObj.Bytes()
	maps.Copy(objs, extras)

	nums := make([]int, 0, len(objs))
	for n := range objs {
		nums = append(nums, n)
	}
	sort.Ints(nums)

	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make(map[int]int)
	for _, n := range nums {
		offsets[n] = out.Len()
		out.Write(objs[n])
	}
	xrefOff := out.Len()
	maxNum := nums[len(nums)-1]
	fmt.Fprintf(&out, "xref\n0 %d\n", maxNum+1)
	out.WriteString("0000000000 65535 f \n")
	for i := 1; i <= maxNum; i++ {
		if off, ok := offsets[i]; ok {
			fmt.Fprintf(&out, "%010d %05d n \n", off, 0)
		} else {
			out.WriteString("0000000000 00000 f \n")
		}
	}
	out.WriteString("trailer\n")
	fmt.Fprintf(&out, "<< /Size %d /Root 1 0 R >>\n", maxNum+1)
	fmt.Fprintf(&out, "startxref\n%d\n%%%%EOF\n", xrefOff)
	return out.Bytes()
}
