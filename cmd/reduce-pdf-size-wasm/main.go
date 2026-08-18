//go:build js && wasm

// Command reduce-pdf-size-wasm exposes pkg/pdf to the browser via
// WebAssembly. It registers a single JavaScript global, reducePdfSize,
// that runs the same recompression logic as the CLI entirely client-side.
package main

import (
	"fmt"
	"syscall/js"

	"github.com/naxmefy/reduce-pdf-size/pkg/pdf"
)

func main() {
	js.Global().Set("reducePdfSize", js.FuncOf(reducePdfSize))
	select {}
}

// reducePdfSize(bytes Uint8Array, quality int, maxDimension int) Promise<Result>
//
// Result is a plain JS object:
//
//	{
//	  data:         Uint8Array (the resulting PDF bytes),
//	  originalSize: number,
//	  newSize:      number,
//	  imagesFound:  number,
//	  recompressed: number,
//	  notes:        string[],
//	}
//
// On failure the promise rejects with an Error whose message describes
// the problem.
func reducePdfSize(this js.Value, args []js.Value) any {
	handler := js.FuncOf(func(_ js.Value, promiseArgs []js.Value) any {
		resolve := promiseArgs[0]
		reject := promiseArgs[1]

		go func() {
			result, err := doReduce(args)
			if err != nil {
				errObj := js.Global().Get("Error").New(err.Error())
				reject.Invoke(errObj)
				return
			}
			resolve.Invoke(result)
		}()

		return nil
	})
	promiseConstructor := js.Global().Get("Promise")
	return promiseConstructor.New(handler)
}

func doReduce(args []js.Value) (js.Value, error) {
	if len(args) < 3 {
		return js.Value{}, fmt.Errorf("reducePdfSize expects (bytes, quality, maxDimension)")
	}

	input := args[0]
	quality := args[1].Int()
	maxDim := args[2].Int()

	buf := make([]byte, input.Get("length").Int())
	js.CopyBytesToGo(buf, input)

	doc, err := pdf.Load(buf)
	if err != nil {
		return js.Value{}, fmt.Errorf("%q is not a supported PDF file: %w", "input", err)
	}

	stats := pdf.RecompressImages(doc, quality, maxDim)

	newBuf := doc.Build()
	if len(newBuf) >= len(buf) {
		newBuf = buf
	}

	outArray := js.Global().Get("Uint8Array").New(len(newBuf))
	js.CopyBytesToJS(outArray, newBuf)

	notes := make([]any, len(stats.Notes))
	for i, n := range stats.Notes {
		notes[i] = n
	}

	result := js.Global().Get("Object").New()
	result.Set("data", outArray)
	result.Set("originalSize", len(buf))
	result.Set("newSize", len(newBuf))
	result.Set("imagesFound", stats.ImagesFound)
	result.Set("recompressed", stats.Recompressed)
	result.Set("notes", js.Global().Get("Array").New(notes...))

	return result, nil
}
