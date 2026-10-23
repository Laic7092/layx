// Main-thread orchestration for the layout-only Web Worker pool.
//
// The pool compiles layx.wasm once, hands the compiled WebAssembly.Module to
// every worker (falling back to per-worker compilation where structured
// cloning of a Module is unsupported), and spreads chapters round-robin. It
// never touches canvas or overlay; workers only run initLayout/layoutChapter.
//
//   const { pool, initMs, moduleShared } = await LayxPool.create({ ... });
//   const out = await pool.layout(chapters, { w, h, size, lh, justify: true });
//   pool.terminate();
//
// Loaded as a classic script: it publishes globalThis.LayxPool and, in Node,
// module.exports, so the self-check can share splitChapters without a Worker.
(function (root) {
  'use strict';

  // Chapters are cut on paragraph boundaries so a chapter is a whole number of
  // "\n"-separated paragraphs. Both the pool and the single-instance baseline
  // in ab.js/bench.go use this split, which makes per-chapter page counts
  // comparable. Small chapters keep round-robin balanced; large ones amortise
  // the per-chapter document build.
  const CHAPTER_CHARS = 8000;

  const now = () => performance.now();

  function splitChapters(text, maxChars = CHAPTER_CHARS) {
    const limit = Math.max(1, Number(maxChars) || CHAPTER_CHARS);
    const out = [];
    let cur = [];
    let chars = 0;
    for (const raw of String(text).split('\n')) {
      const para = raw.trim();
      if (!para) continue;
      // Do not split inside a paragraph: a single oversized paragraph becomes
      // its own chapter.
      if (cur.length && chars + para.length > limit) {
        out.push(cur.join('\n'));
        cur = [];
        chars = 0;
      }
      cur.push(para);
      chars += para.length + 1;
    }
    if (cur.length) out.push(cur.join('\n'));
    return out;
  }

  function clampWorkers(n) {
    const v = Math.floor(Number(n));
    if (!Number.isFinite(v) || v < 1) return 1;
    // Do not hard-code 8: the caller decides, the pool only guards the bounds.
    return Math.min(32, v);
  }

  // Mobile devices are memory-constrained (each worker keeps its own Go heap
  // and font tables), so the default drops to one worker there.
  function defaultWorkers() {
    const nav = root.navigator || {};
    const ua = String(nav.userAgent || '');
    const coarse = !!(root.matchMedia && root.matchMedia('(pointer: coarse)').matches);
    const mobile = coarse || /Android|iPhone|iPad|iPod|Mobile|Silk|Kindle/i.test(ua);
    if (mobile) return 1;
    return clampWorkers(Math.min(4, nav.hardwareConcurrency || 4));
  }

  // Feature probe for "can a Worker receive this WebAssembly.Module?". Firefox
  // reports false; the pool then has every worker compile the bytes itself.
  function canCloneModule(module) {
    if (!module || typeof root.structuredClone !== 'function') return false;
    try {
      root.structuredClone(module);
      return true;
    } catch (_) {
      return false;
    }
  }

  class LayoutPool {
    constructor(opts) {
      this.size = clampWorkers(opts.workers);
      this.workerURL = opts.workerURL || 'layout-worker.js';
      this.wasmURL = opts.wasmURL || 'layx.wasm';
      this.wasmExecURL = opts.wasmExecURL || 'wasm_exec.js';
      this.fontBytes = opts.fontBytes;
      this.fallbackChapter = opts.fallbackChapter || null;

      this.slots = [];
      this.pending = new Map();
      this.seq = 0;
      this.closed = false;
      this.fallbacks = 0;
      this.fallbackMs = 0;
    }

    // create compiles the module (unless one is supplied), starts `workers`
    // instances and measures the whole pool initialisation separately from
    // later layout time.
    static async create(opts = {}) {
      const started = now();
      const pool = new LayoutPool(opts);
      if (!pool.fontBytes || !pool.fontBytes.byteLength) {
        throw new Error('LayoutPool: fontBytes is required');
      }

      let module = opts.module || null;
      let compileMs = 0;
      if (!module && opts.compile !== false) {
        const t0 = now();
        module = await WebAssembly.compile(await fetchBytes(pool.wasmURL));
        compileMs = now() - t0;
      }

      // `shareModule: false` forces the per-worker compilation path; it is a
      // test hook for browsers/runs where Module cloning must be simulated.
      let moduleShared = opts.shareModule !== false && !!module && canCloneModule(module);
      let infos;
      try {
        infos = await Promise.all(
          Array.from({ length: pool.size }, (_, i) => pool._spawn(i, moduleShared ? module : null)),
        );
      } catch (err) {
        if (!moduleShared) {
          await pool.terminate();
          throw err;
        }
        // A worker refused the cloned Module (or failed to start with it).
        // Rebuild the pool with the portable path: every worker compiles.
        moduleShared = false;
        await pool.terminate();
        pool.closed = false;
        pool.slots = [];
        pool.pending.clear();
        infos = await Promise.all(
          Array.from({ length: pool.size }, (_, i) => pool._spawn(i, null)),
        );
      }

      pool.moduleShared = moduleShared;
      pool.compileMs = compileMs;
      pool.workerInitMs = infos.map((x) => x.initMs || 0);
      pool.workerWallMs = infos.map((x) => x.wallMs || 0);
      pool.workerCompileMs = infos.map((x) => x.compileMs || 0);
      pool.faces = infos.map((x) => x.faces || 0);
      pool.initMs = now() - started;
      return pool;
    }

    _spawn(index, module) {
      return new Promise((resolve, reject) => {
        let worker;
        try {
          worker = new root.Worker(this.workerURL);
        } catch (err) {
          reject(err);
          return;
        }
        const slot = { index, worker, ready: false, dead: false };
        this.slots[index] = slot;

        const fail = (err) => {
          slot.dead = true;
          this._rejectSlot(index, err);
          try {
            worker.terminate();
          } catch (_) {}
          reject(err);
        };

        const onInit = (ev) => {
          const msg = ev.data;
          if (!msg || typeof msg !== 'object') return;
          if (msg.type === 'ready') {
            slot.ready = true;
            worker.removeEventListener('message', onInit);
            worker.removeEventListener('error', onInitError);
            worker.addEventListener('message', (e) => this._onMessage(index, e));
            worker.addEventListener('error', (e) => this._onWorkerError(index, e));
            resolve(msg);
            return;
          }
          if (msg.type === 'error') {
            fail(new Error(msg.error || 'worker init failed'));
          }
        };
        const onInitError = (ev) => {
          fail(new Error('worker failed to load: ' + (ev && ev.message ? ev.message : 'error')));
        };
        worker.addEventListener('message', onInit);
        worker.addEventListener('error', onInitError);

        try {
          // Every worker gets its own copy of the font: Go heaps cannot share,
          // and each worker must run CopyBytesToGo + NewFontSet itself.
          const copy = new Uint8Array(this.fontBytes.byteLength);
          copy.set(this.fontBytes);
          worker.postMessage(
            {
              type: 'init',
              module: module || undefined,
              wasmURL: this.wasmURL,
              wasmExecURL: this.wasmExecURL,
              fontBytes: copy.buffer,
            },
            [copy.buffer],
          );
        } catch (err) {
          fail(err);
        }
      });
    }

    _onMessage(index, ev) {
      const msg = ev.data;
      if (!msg || typeof msg !== 'object') return;
      if (msg.type !== 'result' && msg.type !== 'error') return;
      const p = this.pending.get(msg.id);
      if (!p) return;
      this.pending.delete(msg.id);
      if (msg.type === 'error') p.reject(new Error(msg.error || 'layout failed'));
      else p.resolve(msg);
    }

    _onWorkerError(index, ev) {
      const slot = this.slots[index];
      if (slot) slot.dead = true;
      const err = new Error('worker ' + index + ' crashed: ' + (ev && ev.message ? ev.message : 'error'));
      this._rejectSlot(index, err);
    }

    _rejectSlot(index, err) {
      for (const [id, p] of this.pending) {
        if (p.index !== index) continue;
        this.pending.delete(id);
        p.reject(err);
      }
    }

    // layout lays out every chapter in order and returns page counts in the
    // same order. The page total is a plain sum, so anchors and page numbers
    // stay deterministic regardless of which worker finished first.
    async layout(chapters, params = {}) {
      if (this.closed) throw new Error('LayoutPool: pool is closed');
      const t0 = now();
      const tasks = chapters.map((text, i) => {
        const slot = this.slots[i % this.size];
        return this._layoutOn(slot, i, text, params);
      });
      const results = await Promise.all(tasks);

      const chapterPages = new Array(chapters.length);
      let pages = 0;
      let workerMs = 0;
      for (let i = 0; i < results.length; i++) {
        const r = results[i];
        chapterPages[i] = r.pages;
        pages += r.pages;
        workerMs += r.ms || 0;
      }
      return {
        pages,
        chapterPages,
        chapters: chapters.length,
        layoutMs: now() - t0,
        workerMs,
        fallbacks: this.fallbacks,
        fallbackMs: this.fallbackMs,
      };
    }

    _layoutOn(slot, index, text, params) {
      if (slot.dead) return this._fallback(index, text, params, new Error('worker ' + slot.index + ' is dead'));
      const id = ++this.seq;
      const promise = new Promise((resolve, reject) => {
        this.pending.set(id, { index: slot.index, resolve, reject });
        try {
          slot.worker.postMessage({
            type: 'layout',
            id,
            text,
            w: params.w,
            h: params.h,
            size: params.size,
            lh: params.lh,
            justify: !!params.justify,
          });
        } catch (err) {
          this.pending.delete(id);
          reject(err);
        }
      });
      // A worker-level error is the only thing that reaches this catch; a
      // per-chapter protocol error is handled in _onMessage.
      return promise.catch((err) => this._fallback(index, text, params, err));
    }

    // Errors are retried once on the main thread (the caller passes a closure
    // over the already-booted single instance). If no fallback is configured,
    // or it fails too, the layout call rejects and the caller can fall back to
    // a full single-instance layout.
    async _fallback(index, text, params, err) {
      if (!this.fallbackChapter) throw err;
      this.fallbacks++;
      const t0 = now();
      const r = await this.fallbackChapter(text, params, index);
      this.fallbackMs += now() - t0;
      return { pages: Number(r.pages), ms: Number(r.ms) || 0, fallback: true };
    }

    terminate() {
      this.closed = true;
      for (const [id, p] of this.pending) {
        p.reject(new Error('LayoutPool: terminated'));
        this.pending.delete(id);
      }
      for (const slot of this.slots) {
        if (!slot || !slot.worker) continue;
        try {
          slot.worker.postMessage({ type: 'close' });
        } catch (_) {}
        try {
          slot.worker.terminate();
        } catch (_) {}
        slot.dead = true;
      }
    }
  }

  async function fetchBytes(url) {
    const href = root.location ? new URL(url, root.location.href).href : url;
    const resp = await fetch(href);
    if (!resp.ok) throw new Error('fetch ' + url + ': HTTP ' + resp.status);
    return resp.arrayBuffer();
  }

  const api = {
    create: (opts) => LayoutPool.create(opts),
    LayoutPool,
    splitChapters,
    clampWorkers,
    defaultWorkers,
    canCloneModule,
    CHAPTER_CHARS,
  };
  root.LayxPool = api;
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
})(typeof globalThis !== 'undefined' ? globalThis : this);
