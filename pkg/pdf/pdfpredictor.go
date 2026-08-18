package pdf

// reversePredictor undoes a PNG (predictor >= 10) or TIFF (predictor == 2)
// prediction filter applied to raw image sample data, as used by
// /DecodeParms when a Flate/LZW-encoded image stream was pre-filtered for
// better compression. predictor <= 1 means no filtering was applied.
//
// colors is the number of color components per pixel, bpc the bits per
// component, and columns the image width in samples (matches /Columns,
// which defaults to /Width for image streams).
func reversePredictor(data []byte, predictor, colors, bpc, columns int) ([]byte, bool) {
	if predictor <= 1 {
		return data, true
	}
	if bpc != 8 {
		// Sub-byte/16-bit predictors are rare for photographic content and
		// not worth the extra complexity/risk here.
		return nil, false
	}

	bytesPerPixel := colors
	rowBytes := colors * columns
	if rowBytes <= 0 {
		return nil, false
	}

	if predictor == 2 {
		if len(data)%rowBytes != 0 {
			return nil, false
		}
		out := make([]byte, len(data))
		copy(out, data)
		for r := 0; r < len(out); r += rowBytes {
			row := out[r : r+rowBytes]
			for i := bytesPerPixel; i < len(row); i++ {
				row[i] += row[i-bytesPerPixel]
			}
		}
		return out, true
	}

	// PNG predictors (10-15): each row is prefixed by a 1-byte filter type.
	stride := rowBytes + 1
	if len(data)%stride != 0 {
		return nil, false
	}
	numRows := len(data) / stride
	out := make([]byte, 0, numRows*rowBytes)
	prevRow := make([]byte, rowBytes)

	for r := range numRows {
		off := r * stride
		filterType := data[off]
		row := make([]byte, rowBytes)
		copy(row, data[off+1:off+1+rowBytes])

		for i := range rowBytes {
			var a, b, c int
			if i >= bytesPerPixel {
				a = int(row[i-bytesPerPixel])
				c = int(prevRow[i-bytesPerPixel])
			}
			b = int(prevRow[i])

			switch filterType {
			case 0: // None
			case 1: // Sub
				row[i] += byte(a)
			case 2: // Up
				row[i] += byte(b)
			case 3: // Average
				row[i] += byte((a + b) / 2)
			case 4: // Paeth
				row[i] += paethPredictor(a, b, c)
			default:
				return nil, false
			}
		}

		out = append(out, row...)
		prevRow = row
	}
	return out, true
}

func paethPredictor(a, b, c int) byte {
	p := a + b - c
	pa := abs(p - a)
	pb := abs(p - b)
	pc := abs(p - c)
	if pa <= pb && pa <= pc {
		return byte(a)
	}
	if pb <= pc {
		return byte(b)
	}
	return byte(c)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
