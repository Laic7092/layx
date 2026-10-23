// Web Worker entry for the layout-only wasm instance used by layout-pool.js.
//
// The worker owns one Go/wasm instance and never touches canvas or overlay:
// it imports wasm_exec.js, instantiates layx.wasm (preferring the main thread's
// pre-compiled WebAssembly.Module), copies the font bytes into its own Go heap
// and then serves layoutChapter requests.
//
// Protocol (see docs/plan-web-parallel.md §1.3):
//   main -> worker  {type:"init", module?, wasmURL, wasmExecURL, fontBytes}
//   worker -> main  {type:"ready", faces, upem, initMs, compileMs, moduleShared}
//   main -> worker  {type:"layout", id, text, w, h, size, lh, justify}
//   worker -> main  {type:"result", id, pages, ms, buildMs, layoutMs}
//   worker -> main  {type:"error", id, error}
//   main -> worker  {type:"close"}
'use strict';

let api = null;
let initSeen = false;

self.onmessage = (ev) => {
  const msg = ev.data;
  if (!msg || typeof msg !== 'object') return;
  switch (msg.type) {
    case 'init':
      if (initSeen) {
        post({ type: 'error', id: null, error: 'worker already initialised' });
        return;
      }
      initSeen = true;
      init(msg);
      return;
    case 'layout':
      layout(msg);
      return;
    case 'close':
      self.close();
      return;
    default:
      post({ type: 'error', id: msg.id ?? null, error: 'unknown message type: ' + msg.type });
  }
};

function post(msg, transfer) {
  try {
    self.postMessage(msg, transfer);
  } catch (err) {
    // Last resort: keep the main thread from hanging on a failed reply.
    self.postMessage({ type: 'error', id: msg.id ?? null, error: String((err && err.message) || err) });
  }
}

function parseReply(name, raw) {
  const out = JSON.parse(raw);
  if (out && out.error) throw new Error(name + ': ' + out.error);
  return out;
}

async function init(msg) {
  const started = performance.now();
  let compileMs = 0;
  let moduleShared = !!msg.module;
  try {
    const wasmExecURL = msg.wasmExecURL || 'wasm_exec.js';
    importScripts(new URL(wasmExecURL, self.location.href).href);

    const go = new Go();
    let instance = null;
    if (msg.module) {
      try {
        instance = new WebAssembly.Instance(msg.module, go.importObject);
      } catch (err) {
        // A cloned module should always work, but if it does not, fall back to
        // compiling the bytes in this worker rather than failing the pool.
        moduleShared = false;
        instance = null;
      }
    }
    if (!instance) {
      const t0 = performance.now();
      const resp = await fetch(new URL(msg.wasmURL || 'layx.wasm', self.location.href).href);
      if (!resp.ok) throw new Error('wasm: HTTP ' + resp.status);
      const bytes = await resp.arrayBuffer();
      const result = await WebAssembly.instantiate(bytes, go.importObject);
      instance = result.instance;
      compileMs = performance.now() - t0;
    }
    go.run(instance);

    const deadline = performance.now() + 30000;
    while (!self.layx) {
      if (performance.now() > deadline) throw new Error('wasm did not expose layx');
      await new Promise((r) => setTimeout(r, 5));
    }
    api = self.layx;

    const fontBytes = toBytes(msg.fontBytes);
    const info = parseReply('initLayout', api.initLayout(fontBytes));
    post({
      type: 'ready',
      faces: info.faces,
      // Workers never parse the outline face the canvas painter needs, so
      // there is no UPEM to report here. The main thread keeps it from boot().
      upem: null,
      initMs: info.initMs,
      wallMs: performance.now() - started,
      compileMs,
      moduleShared,
    });
  } catch (err) {
    post({ type: 'error', id: null, error: 'init: ' + String((err && err.message) || err) });
  }
}

function toBytes(data) {
  if (!data) return new Uint8Array(0);
  if (data instanceof ArrayBuffer) return new Uint8Array(data);
  if (ArrayBuffer.isView(data)) return new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
  throw new Error('fontBytes must be an ArrayBuffer or typed array');
}

function layout(msg) {
  if (!api) {
    post({ type: 'error', id: msg.id, error: 'worker not initialised' });
    return;
  }
  try {
    const r = parseReply('layoutChapter', api.layoutChapter(
      msg.text, msg.w, msg.h, msg.size, msg.lh, !!msg.justify,
    ));
    post({
      type: 'result',
      id: msg.id,
      pages: r.pages,
      ms: r.ms,
      buildMs: r.buildMs,
      layoutMs: r.layoutMs,
    });
  } catch (err) {
    post({ type: 'error', id: msg.id, error: String((err && err.message) || err) });
  }
}
