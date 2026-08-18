// Command reduce-pdf-size reduces the file size of a PDF, similar to the
// "Reduce File Size" Quartz filter in macOS Preview: embedded JPEG images
// are recompressed at a lower quality and large images are downsampled.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/naxmefy/reduce-pdf-size/pkg/pdf"
)

func usage() {
	_, _ = fmt.Fprintf(os.Stderr, "Usage: %s [-f] <input.pdf> <output.pdf>\n", os.Args[0])
	flag.PrintDefaults()
}

func main() {
	force := flag.Bool("f", false, "overwrite an existing output file")
	quality := flag.Int("quality", 45, "JPEG quality for recompression (1-100)")
	maxDim := flag.Int("max-dimension", 2048, "maximum image edge length in pixels (0 = no downsampling)")
	verbose := flag.Bool("verbose", false, "print details for every image found")
	flag.Usage = usage
	flag.Parse()

	if flag.NArg() != 2 {
		usage()
		os.Exit(2)
	}
	inputPath := flag.Arg(0)
	outputPath := flag.Arg(1)

	if err := run(inputPath, outputPath, *force, *quality, *maxDim, *verbose); err != nil {
		_, err := fmt.Fprintln(os.Stderr, "Error:", err)
		if err != nil {
			log.Fatal(err)
		}
		os.Exit(1)
	}
}

func run(inputPath, outputPath string, force bool, quality, maxDim int, verbose bool) error {
	info, err := os.Stat(inputPath)
	if err != nil {
		return fmt.Errorf("input file %q not found: %w", inputPath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("input path %q is a directory, not a file", inputPath)
	}

	if _, err := os.Stat(outputPath); err == nil {
		if !force {
			return fmt.Errorf("output file %q already exists (use -f to overwrite)", outputPath)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("could not check output file %q: %w", outputPath, err)
	}

	buf, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("could not read input file: %w", err)
	}

	doc, err := pdf.Load(buf)
	if err != nil {
		return fmt.Errorf("%q is not a supported PDF file: %w", inputPath, err)
	}

	stats := pdf.RecompressImages(doc, quality, maxDim)

	newBuf := doc.Build()
	if len(newBuf) >= len(buf) {
		newBuf = buf
	}

	if err := os.WriteFile(outputPath, newBuf, 0o644); err != nil {
		return fmt.Errorf("could not write output file: %w", err)
	}

	origSize := len(buf)
	newSize := len(newBuf)
	reduction := 0.0
	if origSize > 0 {
		reduction = 100 * (1 - float64(newSize)/float64(origSize))
	}

	if verbose {
		for _, n := range stats.Notes {
			fmt.Println(n)
		}
	}
	fmt.Printf("Images found: %d, recompressed: %d\n", stats.ImagesFound, stats.Recompressed)
	fmt.Printf("Original size: %d bytes\n", origSize)
	fmt.Printf("New size:      %d bytes\n", newSize)
	fmt.Printf("Reduction:     %.1f%%\n", reduction)

	return nil
}
