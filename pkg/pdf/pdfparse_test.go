package pdf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"testing"
)

func TestScanObjectsBasic(t *testing.T) {
	buf := []byte("%PDF-1.4\n" +
		"1 0 obj\n<< /Type /Catalog >>\nendobj\n" +
		"2 0 obj\n<< /Type /Pages >>\nendobj\n")
	raw, gen := scanObjects(buf)
	if len(raw) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(raw))
	}
	if gen[1] != 0 || gen[2] != 0 {
		t.Fatalf("unexpected generations: %v", gen)
	}
	if !bytes.Contains(raw[1], []byte("/Catalog")) {
		t.Fatalf("obj 1 content wrong: %s", raw[1])
	}
}

func TestScanObjectsIncrementalUpdateLastWins(t *testing.T) {
	buf := []byte("%PDF-1.4\n" +
		"1 0 obj\n<< /V 1 >>\nendobj\n" +
		"1 0 obj\n<< /V 2 >>\nendobj\n")
	raw, _ := scanObjects(buf)
	if !bytes.Contains(raw[1], []byte("/V 2")) {
		t.Fatalf("expected later definition to win, got %s", raw[1])
	}
}

func TestStreamSpanDirectLength(t *testing.T) {
	raw := []byte("4 0 obj\n<< /Length 5 >>\nstream\nHELLOxxxx\nendstream\nendobj\n")
	dictPart := extractDictPart(raw)
	start, end, ok := streamSpan(raw, dictPart)
	if !ok {
		t.Fatal("expected ok")
	}
	if got := string(raw[start:end]); got != "HELLO" {
		t.Fatalf("got %q", got)
	}
}

func TestStreamSpanIndirectLengthFallsBackToEndstream(t *testing.T) {
	raw := []byte("4 0 obj\n<< /Length 9 0 R >>\nstream\nHELLO\nendstream\nendobj\n")
	dictPart := extractDictPart(raw)
	start, end, ok := streamSpan(raw, dictPart)
	if !ok {
		t.Fatal("expected ok")
	}
	if got := string(raw[start:end]); got != "HELLO" {
		t.Fatalf("got %q", got)
	}
}

func TestExpandObjectStreamsExtractsCompressedObjects(t *testing.T) {
	catalog := []byte("<< /Type /Catalog /Pages 2 0 R >>")
	pages := []byte("<< /Type /Pages /Kids [] /Count 0 >>")

	var header bytes.Buffer
	var body bytes.Buffer
	fmt.Fprintf(&header, "1 %d ", body.Len())
	body.Write(catalog)
	body.WriteByte(' ')
	fmt.Fprintf(&header, "2 %d ", body.Len())
	body.Write(pages)
	body.WriteByte(' ')

	first := header.Len()
	var uncompressed bytes.Buffer
	uncompressed.Write(header.Bytes())
	uncompressed.Write(body.Bytes())

	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	zw.Write(uncompressed.Bytes())
	zw.Close()

	var objStm bytes.Buffer
	fmt.Fprintf(&objStm, "6 0 obj\n<< /Type /ObjStm /N 2 /First %d /Filter /FlateDecode /Length %d >>\nstream\n",
		first, compressed.Len())
	objStm.Write(compressed.Bytes())
	objStm.WriteString("\nendstream\nendobj\n")

	raw := map[int][]byte{6: objStm.Bytes()}
	gen := map[int]int{6: 0}
	skip := expandObjectStreams(raw, gen)

	if !skip[6] {
		t.Fatal("expected ObjStm container to be marked skipped")
	}
	if _, ok := raw[1]; !ok {
		t.Fatal("expected object 1 to be extracted")
	}
	if !bytes.Contains(raw[1], []byte("/Catalog")) {
		t.Fatalf("obj 1 wrong: %s", raw[1])
	}
	if !bytes.Contains(raw[2], []byte("/Pages")) {
		t.Fatalf("obj 2 wrong: %s", raw[2])
	}
}

func TestExpandObjectStreamsLooseDefinitionWins(t *testing.T) {
	// Incremental update: object 1 is both compressed (stale) and present
	// loose (fresh, e.g. from an incremental update). The loose one must win.
	stale := []byte("<< /V 1 >>")
	var header bytes.Buffer
	fmt.Fprintf(&header, "1 0 ")
	first := header.Len()
	var uncompressed bytes.Buffer
	uncompressed.Write(header.Bytes())
	uncompressed.Write(stale)

	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	zw.Write(uncompressed.Bytes())
	zw.Close()

	var objStm bytes.Buffer
	fmt.Fprintf(&objStm, "6 0 obj\n<< /Type /ObjStm /N 1 /First %d /Filter /FlateDecode /Length %d >>\nstream\n",
		first, compressed.Len())
	objStm.Write(compressed.Bytes())
	objStm.WriteString("\nendstream\nendobj\n")

	raw := map[int][]byte{
		6: objStm.Bytes(),
		1: []byte("1 0 obj\n<< /V 2 >>\nendobj\n"),
	}
	gen := map[int]int{6: 0, 1: 0}
	expandObjectStreams(raw, gen)

	if !bytes.Contains(raw[1], []byte("/V 2")) {
		t.Fatalf("expected loose definition to win, got %s", raw[1])
	}
}

func TestLoadPDFRejectsEncrypted(t *testing.T) {
	buf := []byte("%PDF-1.4\n1 0 obj\n<< >>\nendobj\ntrailer\n<< /Root 1 0 R /Encrypt 2 0 R >>\n")
	if _, err := LoadPDF(buf); err == nil {
		t.Fatal("expected error for encrypted PDF")
	}
}

func TestLoadPDFRejectsNonPDF(t *testing.T) {
	if _, err := LoadPDF([]byte("not a pdf")); err == nil {
		t.Fatal("expected error for non-PDF input")
	}
}

func TestLoadPDFFindsRootAndInfo(t *testing.T) {
	pdfBytes := buildJPEGImagePDF(t, 20, 20, 90)
	doc, err := LoadPDF(pdfBytes)
	if err != nil {
		t.Fatalf("LoadPDF: %v", err)
	}
	if doc.rootNum != 1 {
		t.Fatalf("expected root object 1, got %d", doc.rootNum)
	}
}

func TestBuildProducesParsableClassicXref(t *testing.T) {
	pdfBytes := buildJPEGImagePDF(t, 20, 20, 90)
	doc, err := LoadPDF(pdfBytes)
	if err != nil {
		t.Fatalf("LoadPDF: %v", err)
	}
	out := doc.Build()

	if !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Fatal("missing PDF header")
	}
	if !bytes.Contains(out, []byte("trailer")) || !bytes.Contains(out, []byte("startxref")) {
		t.Fatal("missing trailer/startxref")
	}

	reloaded, err := LoadPDF(out)
	if err != nil {
		t.Fatalf("rebuilt PDF failed to parse: %v", err)
	}
	if len(reloaded.raw) != len(doc.raw) {
		t.Fatalf("object count mismatch after round-trip: got %d want %d", len(reloaded.raw), len(doc.raw))
	}
}
