package pdf

import (
	"bytes"
	"compress/zlib"
	"strconv"
	"testing"
)

func TestResolveColorSpaceNamed(t *testing.T) {
	doc := &pdfDoc{raw: map[int][]byte{}}

	cs := resolveColorSpace(doc, []byte("/DeviceRGB"))
	if !cs.ok || cs.kind != "rgb" || cs.ncomp != 3 {
		t.Fatalf("DeviceRGB: got %+v", cs)
	}

	cs = resolveColorSpace(doc, []byte("/DeviceGray"))
	if !cs.ok || cs.kind != "gray" || cs.ncomp != 1 {
		t.Fatalf("DeviceGray: got %+v", cs)
	}

	cs = resolveColorSpace(doc, []byte("/DeviceCMYK"))
	if cs.ok {
		t.Fatalf("DeviceCMYK should be unsupported, got %+v", cs)
	}
}

func TestResolveColorSpaceICCBasedIndirect(t *testing.T) {
	doc := &pdfDoc{raw: map[int][]byte{
		5: []byte("5 0 obj\n<< /N 3 /Alternate /DeviceRGB /Length 0 >>\nstream\n\nendstream\nendobj\n"),
		6: []byte("6 0 obj\n[/ICCBased 5 0 R]\nendobj\n"),
	}}

	cs := resolveColorSpace(doc, []byte("6 0 R"))
	if !cs.ok || cs.kind != "rgb" || cs.ncomp != 3 {
		t.Fatalf("got %+v", cs)
	}
}

func TestResolveColorSpaceICCBasedGrayDirectObject(t *testing.T) {
	// Some writers point /ColorSpace directly at the ICCBased stream object
	// rather than at an intermediate array object.
	doc := &pdfDoc{raw: map[int][]byte{
		5: []byte("5 0 obj\n<< /N 1 /Length 0 >>\nstream\n\nendstream\nendobj\n"),
	}}
	cs := resolveColorSpace(doc, []byte("5 0 R"))
	if !cs.ok || cs.kind != "gray" || cs.ncomp != 1 {
		t.Fatalf("got %+v", cs)
	}
}

func TestResolveColorSpaceIndexedWithStreamLookup(t *testing.T) {
	palette := []byte{
		255, 0, 0, // index 0: red
		0, 255, 0, // index 1: green
		0, 0, 255, // index 2: blue
	}
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	zw.Write(palette)
	zw.Close()

	doc := &pdfDoc{raw: map[int][]byte{
		9: []byte("9 0 obj\n<< /Filter /FlateDecode /Length " +
			strconv.Itoa(compressed.Len()) + " >>\nstream\n" + compressed.String() + "\nendstream\nendobj\n"),
	}}

	cs := resolveColorSpace(doc, []byte("[/Indexed /DeviceRGB 2 9 0 R]"))
	if !cs.ok || !cs.indexed || cs.baseKind != "rgb" || cs.baseN != 3 {
		t.Fatalf("got %+v", cs)
	}
	if !bytes.Equal(cs.palette, palette) {
		t.Fatalf("palette mismatch: got %v want %v", cs.palette, palette)
	}
}

func TestResolveColorSpaceIndexedWithInlineString(t *testing.T) {
	doc := &pdfDoc{raw: map[int][]byte{}}
	cs := resolveColorSpace(doc, []byte(`[/Indexed /DeviceGray 1 (\000\377)]`))
	if !cs.ok || !cs.indexed || cs.baseKind != "gray" {
		t.Fatalf("got %+v", cs)
	}
	if len(cs.palette) != 2 || cs.palette[0] != 0 || cs.palette[1] != 0xff {
		t.Fatalf("palette decode wrong: %v", cs.palette)
	}
}

func TestResolveColorSpaceUnsupportedFamilies(t *testing.T) {
	doc := &pdfDoc{raw: map[int][]byte{}}
	for _, v := range [][]byte{
		[]byte("/Pattern"),
		[]byte("[/Separation /Spot /DeviceCMYK 4 0 R]"),
		[]byte("[/Lab << >>]"),
	} {
		if cs := resolveColorSpace(doc, v); cs.ok {
			t.Fatalf("%q: expected unsupported, got %+v", v, cs)
		}
	}
}
