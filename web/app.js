const dropZone = document.getElementById("drop");
const fileInput = document.getElementById("file-input");
const dropLabel = document.getElementById("drop-label");
const qualityInput = document.getElementById("quality");
const qualityValue = document.getElementById("quality-value");
const maxDimensionInput = document.getElementById("max-dimension");
const maxDimensionValue = document.getElementById("max-dimension-value");
const applyButton = document.getElementById("apply-button");
const statusEl = document.getElementById("status");
const spinnerEl = document.getElementById("spinner");
const statusTextEl = document.getElementById("status-text");
const resultEl = document.getElementById("result");
const resultSummary = document.getElementById("result-summary");
const resultNotes = document.getElementById("result-notes");
const downloadLink = document.getElementById("download-link");

let currentFile = null;
let currentDownloadUrl = null;

let worker = null;
let nextRequestId = 0;
let pendingRequest = null;

function getWorker() {
  if (worker) return worker;
  worker = new Worker("worker.js");
  worker.onmessage = (e) => {
    const msg = e.data;
    if (!pendingRequest || msg.id !== pendingRequest.id) return;
    if (msg.phase) {
      pendingRequest.onPhase(msg.phase);
      return;
    }
    const { resolve, reject } = pendingRequest;
    pendingRequest = null;
    if (msg.ok) resolve(msg);
    else reject(new Error(msg.error));
  };
  worker.onerror = (e) => {
    if (!pendingRequest) return;
    const { reject } = pendingRequest;
    pendingRequest = null;
    reject(new Error(e.message || "Worker error"));
  };
  return worker;
}

// Runs the reduction in a Worker (its own thread) rather than on the main
// thread, so a large PDF can't freeze the page or trigger the browser's
// "page unresponsive" prompt.
function reduceInWorker(arrayBuffer, quality, maxDimension, onPhase) {
  const w = getWorker();
  const id = ++nextRequestId;
  return new Promise((resolve, reject) => {
    pendingRequest = { id, resolve, reject, onPhase };
    w.postMessage({ id, buf: arrayBuffer, quality, maxDimension }, [arrayBuffer]);
  });
}

qualityInput.addEventListener("input", () => {
  qualityValue.textContent = qualityInput.value;
  showApplyButtonIfNeeded();
});
maxDimensionInput.addEventListener("input", () => {
  maxDimensionValue.textContent = maxDimensionInput.value;
  showApplyButtonIfNeeded();
});
applyButton.addEventListener("click", () => {
  if (currentFile) processFile(currentFile);
});

function showApplyButtonIfNeeded() {
  if (currentFile) applyButton.hidden = false;
}

dropZone.addEventListener("dragover", (e) => {
  e.preventDefault();
  dropZone.classList.add("dragover");
});
dropZone.addEventListener("dragleave", () => {
  dropZone.classList.remove("dragover");
});
dropZone.addEventListener("drop", (e) => {
  e.preventDefault();
  dropZone.classList.remove("dragover");
  const file = e.dataTransfer.files[0];
  if (file) selectFile(file);
});
fileInput.addEventListener("change", () => {
  const file = fileInput.files[0];
  if (file) selectFile(file);
});

function selectFile(file) {
  currentFile = file;
  applyButton.hidden = true;
  processFile(file);
}

function setStatus(message, isError) {
  statusEl.hidden = !message;
  statusTextEl.textContent = message || "";
  statusEl.classList.toggle("error", Boolean(isError));
  spinnerEl.hidden = !message || Boolean(isError);
}

function formatBytes(n) {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(2)} MB`;
}

async function processFile(file) {
  if (file.type !== "application/pdf" && !file.name.toLowerCase().endsWith(".pdf")) {
    setStatus("Please choose a PDF file.", true);
    return;
  }

  applyButton.hidden = true;
  applyButton.disabled = true;
  resultEl.hidden = true;
  dropLabel.textContent = file.name;
  setStatus("Loading engine…");

  try {
    const arrayBuffer = await file.arrayBuffer();
    const quality = Number(qualityInput.value);
    const maxDimension = Number(maxDimensionInput.value);

    const out = await reduceInWorker(arrayBuffer, quality, maxDimension, (phase) => {
      setStatus(phase === "loading" ? "Loading engine…" : "Reducing…");
    });

    const reduction = out.originalSize > 0
      ? 100 * (1 - out.newSize / out.originalSize)
      : 0;

    resultSummary.textContent =
      `Images found: ${out.imagesFound}, recompressed: ${out.recompressed}. ` +
      `${formatBytes(out.originalSize)} → ${formatBytes(out.newSize)} ` +
      `(${reduction.toFixed(1)}% smaller).`;

    resultNotes.innerHTML = "";
    for (const note of out.notes || []) {
      const li = document.createElement("li");
      li.textContent = note;
      resultNotes.appendChild(li);
    }

    if (currentDownloadUrl) URL.revokeObjectURL(currentDownloadUrl);
    const blob = new Blob([out.data], { type: "application/pdf" });
    currentDownloadUrl = URL.createObjectURL(blob);
    downloadLink.href = currentDownloadUrl;
    downloadLink.download = file.name.replace(/\.pdf$/i, "") + "-reduced.pdf";

    resultEl.hidden = false;
    setStatus("");
  } catch (err) {
    console.error(err);
    setStatus(String(err.message || err), true);
  } finally {
    applyButton.disabled = false;
  }
}
