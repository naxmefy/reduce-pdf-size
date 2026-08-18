// Runs the wasm engine on its own thread so heavy recompression never
// blocks the page's main thread (which would otherwise make the browser
// think the tab has hung and offer to kill it).
importScripts("wasm_exec.js");

let wasmReady = null;

function loadWasm(onLoading) {
  if (wasmReady) return wasmReady;
  onLoading();
  const go = new Go();
  wasmReady = WebAssembly.instantiateStreaming(fetch("reduce-pdf-size.wasm"), go.importObject)
    .then((result) => {
      go.run(result.instance);
    });
  return wasmReady;
}

self.onmessage = async (e) => {
  const { id, buf, quality, maxDimension } = e.data;
  try {
    await loadWasm(() => self.postMessage({ id, phase: "loading" }));
    self.postMessage({ id, phase: "reducing" });

    const input = new Uint8Array(buf);
    const out = await self.reducePdfSize(input, quality, maxDimension);

    self.postMessage({
      id,
      ok: true,
      data: out.data.buffer,
      originalSize: out.originalSize,
      newSize: out.newSize,
      imagesFound: out.imagesFound,
      recompressed: out.recompressed,
      notes: out.notes,
    }, [out.data.buffer]);
  } catch (err) {
    self.postMessage({ id, ok: false, error: String((err && err.message) || err) });
  }
};
