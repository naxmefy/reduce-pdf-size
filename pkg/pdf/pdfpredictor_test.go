package pdf

import (
	"bytes"
	"testing"
)

func TestReversePredictorNoop(t *testing.T) {
	data := []byte{1, 2, 3, 4}
	out, ok := reversePredictor(data, 1, 3, 8, 10)
	if !ok || !bytes.Equal(out, data) {
		t.Fatalf("expected passthrough, got %v ok=%v", out, ok)
	}
}

func TestReversePredictorTIFF(t *testing.T) {
	// 2 pixels, 3 components (RGB), row = [10,20,30, 15,25,35]
	original := []byte{10, 20, 30, 15, 25, 35}
	filtered := make([]byte, len(original))
	copy(filtered, original)
	// TIFF horizontal differencing: each sample minus the sample "colors"
	// positions back in the same row.
	for i := len(filtered) - 1; i >= 3; i-- {
		filtered[i] = filtered[i] - filtered[i-3]
	}

	out, ok := reversePredictor(filtered, 2, 3, 8, 2)
	if !ok {
		t.Fatal("expected ok")
	}
	if !bytes.Equal(out, original) {
		t.Fatalf("got %v want %v", out, original)
	}
}

func TestReversePredictorPNGUp(t *testing.T) {
	row0 := []byte{10, 20, 30, 40, 50, 60}
	row1 := []byte{12, 18, 33, 41, 48, 65}

	var filtered bytes.Buffer
	filtered.WriteByte(0) // None
	filtered.Write(row0)
	filtered.WriteByte(2) // Up
	for i, b := range row1 {
		filtered.WriteByte(b - row0[i])
	}

	out, ok := reversePredictor(filtered.Bytes(), 15, 3, 8, 2)
	if !ok {
		t.Fatal("expected ok")
	}
	want := append(append([]byte{}, row0...), row1...)
	if !bytes.Equal(out, want) {
		t.Fatalf("got %v want %v", out, want)
	}
}

func TestReversePredictorPNGSubAndAverage(t *testing.T) {
	rowBytes := 3
	row0 := []byte{100, 150, 200}

	var filtered bytes.Buffer
	// Sub filter on the very first row (prevRow is all zero).
	filtered.WriteByte(1)
	for i, b := range row0 {
		var left byte
		if i >= 1 {
			left = row0[i-1]
		}
		filtered.WriteByte(b - left)
	}

	out, ok := reversePredictor(filtered.Bytes(), 11, 1, 8, rowBytes)
	if !ok {
		t.Fatal("expected ok")
	}
	if !bytes.Equal(out, row0) {
		t.Fatalf("got %v want %v", out, row0)
	}
}

func TestReversePredictorPNGPaeth(t *testing.T) {
	rowBytes := 4
	row0 := []byte{5, 10, 15, 20}
	row1 := []byte{7, 9, 20, 25}

	var filtered bytes.Buffer
	filtered.WriteByte(0)
	filtered.Write(row0)
	filtered.WriteByte(4) // Paeth
	for i := range row1 {
		var a, c int
		if i >= 1 {
			a = int(row1[i-1])
			c = int(row0[i-1])
		}
		b := int(row0[i])
		pred := paethPredictor(a, b, c)
		filtered.WriteByte(row1[i] - pred)
	}

	out, ok := reversePredictor(filtered.Bytes(), 15, 1, 8, rowBytes)
	if !ok {
		t.Fatal("expected ok")
	}
	want := append(append([]byte{}, row0...), row1...)
	if !bytes.Equal(out, want) {
		t.Fatalf("got %v want %v", out, want)
	}
}

func TestReversePredictorRejectsNon8Bit(t *testing.T) {
	_, ok := reversePredictor([]byte{1, 2, 3}, 15, 3, 4, 10)
	if ok {
		t.Fatal("expected rejection for non-8-bit predictor input")
	}
}

func TestReversePredictorRejectsMisalignedData(t *testing.T) {
	_, ok := reversePredictor([]byte{1, 2, 3, 4, 5}, 2, 3, 8, 2)
	if ok {
		t.Fatal("expected rejection for data not aligned to row size")
	}
}
