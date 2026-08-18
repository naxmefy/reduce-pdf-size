# reduce-pdf-size

A command-line tool that reduces the file size of PDFs — similar to the "Reduce File Size" Quartz filter in macOS Preview. Embedded images are recompressed at a lower quality and large images are downsampled if needed.

## How it works

`reduce-pdf-size` walks every object in a PDF file looking for embedded images and handles two cases:

- **Images already encoded as JPEG (DCTDecode)** are decoded and recompressed at a lower quality.
- **Uncompressed or Flate-compressed raster images** (DeviceGray/DeviceRGB, including via ICCBased/Indexed) are converted to JPEG.

If requested, images are downsampled to a maximum edge length beforehand. An object is only replaced if the result is actually smaller than the original. Images with `/ImageMask true`, as well as objects with unsupported filters, are left untouched. If the resulting PDF ends up no smaller than the original overall, the original file is written back unchanged.

## Installation

Requires [Go](https://go.dev/) 1.26 or newer.

```bash
go build -o reduce-pdf-size ./cmd/reduce-pdf-size
```

Or using the provided Makefile:

```bash
make build
```

The binary is then placed at `tmp/reduce-pdf-size`.

### Building for multiple platforms

```bash
make build-all
```

Builds binaries for Linux, macOS, and Windows (amd64/arm64) and places them in `tmp/` as well.

## Usage

```bash
reduce-pdf-size [-f] [-quality N] [-max-dimension N] [-verbose] <input.pdf> <output.pdf>
```

### Options

| Flag              | Default  | Description                                                  |
|-------------------|----------|----------------------------------------------------------------|
| `-f`              | `false`  | Overwrite the target file if it already exists                 |
| `-quality`        | `45`     | JPEG quality for recompression (1–100)                          |
| `-max-dimension`  | `2048`   | Maximum image edge length in pixels (`0` = no downsampling)     |
| `-verbose`        | `false`  | Print details about every image found                          |

### Example

```bash
reduce-pdf-size -quality 60 -max-dimension 1600 input.pdf output.pdf
```

Output:

```
Images found: 12, recompressed: 9
Original size: 8452012 bytes
New size:      2103456 bytes
Reduction:     75.1%
```

## Web demo

The same recompression logic runs entirely client-side in the browser via
WebAssembly — see [`cmd/reduce-pdf-size-wasm`](cmd/reduce-pdf-size-wasm) and
[`web/`](web). No file is ever uploaded anywhere; it's deployed via GitHub
Pages on every push to `main` (see
[`.github/workflows/pages.yml`](.github/workflows/pages.yml)).

## Tests

```bash
go test ./...
```

## License

[MIT](LICENSE)
