package pdf

import (
	"bytes"
	"testing"
)

func TestReadValueAtName(t *testing.T) {
	buf := []byte("  /DeviceRGB rest")
	start, end, ok := readValueAt(buf, 0)
	if !ok {
		t.Fatal("expected ok")
	}
	if got := string(buf[start:end]); got != "/DeviceRGB" {
		t.Fatalf("got %q", got)
	}
}

func TestReadValueAtArray(t *testing.T) {
	buf := []byte("[/Indexed /DeviceRGB 255 6 0 R] tail")
	start, end, ok := readValueAt(buf, 0)
	if !ok {
		t.Fatal("expected ok")
	}
	want := "[/Indexed /DeviceRGB 255 6 0 R]"
	if got := string(buf[start:end]); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestReadValueAtNestedArray(t *testing.T) {
	buf := []byte("[1 2 [3 4] 5] tail")
	start, end, ok := readValueAt(buf, 0)
	if !ok || string(buf[start:end]) != "[1 2 [3 4] 5]" {
		t.Fatalf("got %q ok=%v", string(buf[start:end]), ok)
	}
}

func TestReadValueAtDict(t *testing.T) {
	buf := []byte("<< /A 1 /B << /C 2 >> >> tail")
	start, end, ok := readValueAt(buf, 0)
	if !ok {
		t.Fatal("expected ok")
	}
	want := "<< /A 1 /B << /C 2 >> >>"
	if got := string(buf[start:end]); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestReadValueAtReference(t *testing.T) {
	buf := []byte("12 0 R /Next")
	start, end, ok := readValueAt(buf, 0)
	if !ok || string(buf[start:end]) != "12 0 R" {
		t.Fatalf("got %q ok=%v", string(buf[start:end]), ok)
	}
}

func TestReadValueAtPlainNumber(t *testing.T) {
	buf := []byte("42 /Next")
	start, end, ok := readValueAt(buf, 0)
	if !ok || string(buf[start:end]) != "42" {
		t.Fatalf("got %q ok=%v", string(buf[start:end]), ok)
	}
}

func TestReadValueAtStringWithEscapedParens(t *testing.T) {
	buf := []byte(`(a \) b \( c) tail`)
	start, end, ok := readValueAt(buf, 0)
	if !ok {
		t.Fatal("expected ok")
	}
	want := `(a \) b \( c)`
	if got := string(buf[start:end]); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDictValueSpanDoesNotMatchPrefixKey(t *testing.T) {
	dict := []byte("<< /FilterX /Foo /Filter /DCTDecode >>")
	_, _, vs, ve, ok := dictValueSpan(dict, "Filter")
	if !ok {
		t.Fatal("expected to find /Filter")
	}
	if got := string(dict[vs:ve]); got != "/DCTDecode" {
		t.Fatalf("got %q, want /DCTDecode (matched /FilterX instead?)", got)
	}
}

func TestReplaceDictValueDirectLength(t *testing.T) {
	dict := []byte("<< /Type /XObject /Length 1234 /Filter /DCTDecode >>")
	out := replaceDictValue(dict, "Length", []byte("99"))
	want := "<< /Type /XObject /Length 99 /Filter /DCTDecode >>"
	if string(out) != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestReplaceDictValueIndirectLength(t *testing.T) {
	dict := []byte("<< /Length 9 0 R /Filter /DCTDecode >>")
	out := replaceDictValue(dict, "Length", []byte("99"))
	want := "<< /Length 99 /Filter /DCTDecode >>"
	if string(out) != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestReplaceDictValueMissingKeyNoOp(t *testing.T) {
	dict := []byte("<< /Type /XObject >>")
	out := replaceDictValue(dict, "Length", []byte("99"))
	if !bytes.Equal(out, dict) {
		t.Fatalf("expected unchanged, got %q", out)
	}
}

func TestRemoveDictKey(t *testing.T) {
	dict := []byte("<< /Filter /FlateDecode /DecodeParms << /Predictor 15 >> /Width 10 >>")
	out := removeDictKey(dict, "DecodeParms")
	want := "<< /Filter /FlateDecode /Width 10 >>"
	if string(out) != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestRemoveDictKeyMissingNoOp(t *testing.T) {
	dict := []byte("<< /Width 10 >>")
	out := removeDictKey(dict, "DecodeParms")
	if !bytes.Equal(out, dict) {
		t.Fatalf("expected unchanged, got %q", out)
	}
}
