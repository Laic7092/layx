// A/B harness: layout the same corpus twice in the same Chromium instance.
//
//   native  = the layx engine compiled to wasm, drawing to a canvas
//   webview = DOM + CSS multi-column, the approach a WebView-based reader uses
//
// Parameters: w, h (viewport incl. 40px margins), size, lh, chars, turns.
// The result is written as JSON into <pre id="result"> and scripts/ab.sh reads
// it out of a --dump-dom run.
(async () => {
  const q = new URLSearchParams(location.search);
  const num = (k, d) => (q.has(k) ? Number(q.get(k)) : d);
  const W = num('w', 640);
  const H = num('h', 900);
  const SIZE = num('size', 18);
  const LH = num('lh', 1.65);
  const CHARS = num('chars', 200000);
  const TURNS = num('turns', 100);
  const MARGIN = 40;
  const CW = W - 2 * MARGIN;
  const CH = H - 2 * MARGIN;

  const started = performance.now();

  const PARAS = [
    '这是一段用来比较排版性能的中文小说正文。主角走进屋子，看见桌上摊着一封没有署名的信。窗外下着雨，院子里积水映着灯光，风把门吹得吱呀作响。他站在那里，很久没有说话。',
    '第一章的名字叫“归途”。作者用了不少短句，也用了几个长句，中间夹着 English words 和数字 2024，用来观察中西文混排与自动间距。标点符号、引号、“这样的段落”、以及省略号……都应当被正确处理。',
    '她问他：“你打算怎么办？”他没有立刻回答，而是把信折好，放回信封里。那两个人在门口等了一会儿，最后决定明天一早再出发。雨停了，街上的灯一盏一盏地灭下去。',
    'A paragraph of plain English follows, with some reasonably long words: extraordinary, misunderstanding, characteristically. It exists so the comparison also covers Latin shaping, justification and line breaking in a mixed document.',
  ];

  function corpus(chars) {
    const out = [];
    let n = 0;
    for (let i = 0; n < chars; i++) {
      const p = PARAS[i % PARAS.length];
      out.push(p);
      n += p.length + 1;
    }
    return out.join('\n').slice(0, chars);
  }

  const text = corpus(CHARS);
  const paragraphs = text.split('\n');

  function pct(v, p) {
    if (!v.length) return 0;
    const s = [...v].sort((a, b) => a - b);
    const i = Math.min(s.length - 1, Math.max(0, Math.ceil((p / 100) * s.length) - 1));
    return s[i];
  }

  function heapMB() {
    const m = performance.memory;
    return m ? Math.round((m.usedJSHeapSize / 1048576) * 10) / 10 : null;
  }

  const heapStart = heapMB();

  // ---------------------------------------------------------------- webview

  function runWebView() {
    const book = document.getElementById('book');
    book.style.left = MARGIN + 'px';
    book.style.top = MARGIN + 'px';
    book.style.width = CW + 'px';
    book.style.height = CH + 'px';
    book.style.columnWidth = CW + 'px';
    book.style.columnGap = '0px';
    book.style.columnFill = 'auto';
    book.style.overflow = 'hidden';
    book.style.fontSize = SIZE + 'px';
    book.style.lineHeight = String(LH);
    book.style.textAlign = 'justify';
    book.innerHTML = '';

    let html = '';
    for (const p of paragraphs) {
      html += '<p style="margin-bottom:' + SIZE * 0.5 + 'px">' + escapeHTML(p) + '</p>';
    }

    const t0 = performance.now();
    book.innerHTML = html;
    void book.scrollWidth; // force layout
    const setupMs = performance.now() - t0;

    const pages = Math.max(1, Math.round(book.scrollWidth / CW));

    const cold = [];
    const hot = [];
    const n = Math.min(TURNS, pages);
    for (let i = 0; i < n; i++) {
      const t = performance.now();
      book.scrollLeft = i * CW;
      void book.scrollLeft;
      cold.push(performance.now() - t);
    }
    for (let i = 0; i < n; i++) {
      const t = performance.now();
      book.scrollLeft = i * CW;
      void book.scrollLeft;
      hot.push(performance.now() - t);
    }

    const tReflow = performance.now();
    book.style.fontSize = '24px';
    void book.scrollWidth;
    const reflowMs = performance.now() - tReflow;
    book.style.fontSize = SIZE + 'px';
    void book.scrollWidth;

    const tRepage = performance.now();
    book.style.lineHeight = String(LH * 1.2);
    void book.scrollWidth;
    const repageMs = performance.now() - tRepage;
    book.style.lineHeight = String(LH);
    void book.scrollWidth;

    const heap = heapMB();

    book.innerHTML = '';
    book.style.overflow = 'visible';
    void book.scrollWidth;

    return {
      setupMs: round(setupMs),
      pages,
      cold: cold.map(round),
      hot: hot.map(round),
      coldP50: round(pct(cold, 50)),
      coldP99: round(pct(cold, 99)),
      hotP50: round(pct(hot, 50)),
      hotP99: round(pct(hot, 99)),
      reflowMs: round(reflowMs),
      repageMs: round(repageMs),
      heapMB: heap,
    };
  }

  // ----------------------------------------------------------------- native

  async function runNative() {
    const canvas = document.getElementById('page');
    const overlay = document.getElementById('overlay');
    canvas.width = CW;
    canvas.height = CH;
    overlay.width = CW;
    overlay.height = CH;

    const tBoot = performance.now();
    const go = new Go();
    const { instance } = await WebAssembly.instantiateStreaming(fetch('layx.wasm'), go.importObject);
    go.run(instance);
    while (!window.layx) await new Promise((r) => setTimeout(r, 5));

    const fontResp = await fetch('fonts/book.font');
    const fontBytes = new Uint8Array(await fontResp.arrayBuffer());
    call('boot', canvas, overlay, fontBytes, 1);
    const bootMs = performance.now() - tBoot;

    const t0 = performance.now();
    const r = call('bench', text, W, H, SIZE, LH, true, TURNS);
    const totalMs = performance.now() - t0;

    return {
      bootMs: round(bootMs),
      buildMs: round(r.buildMs),
      layoutMs: round(r.layoutMs),
      pages: r.pages,
      totalMs: round(totalMs),
      cold: r.coldMs.map(round),
      hot: r.hotMs.map(round),
      coldP50: round(pct(r.coldMs, 50)),
      coldP99: round(pct(r.coldMs, 99)),
      hotP50: round(pct(r.hotMs, 50)),
      hotP99: round(pct(r.hotMs, 99)),
      reflowMs: round(r.reflowMs),
      repageMs: round(r.repageMs),
      wasmMB: Math.round((r.memSys / 1048576) * 10) / 10,
      heapMB: heapMB(),
    };
  }

  function call(name, ...args) {
    const out = JSON.parse(window.layx[name](...args));
    if (out && out.error) throw new Error(name + ': ' + out.error);
    return out;
  }

  function round(v) {
    return Math.round(v * 100) / 100;
  }

  function escapeHTML(s) {
    return s.replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));
  }

  // The WebView side needs the same font file the wasm side parses. Its load
  // time belongs to the WebView boot cost just as font parsing belongs to the
  // wasm boot cost.
  const tFont = performance.now();
  const face = new FontFace('book', 'url(fonts/book.font)');
  await face.load();
  document.fonts.add(face);
  await document.fonts.ready;
  const fontLoadMs = performance.now() - tFont;

  const webview = runWebView();
  webview.fontLoadMs = round(fontLoadMs);
  const native = await runNative();

  const out = {
    params: { w: W, h: H, size: SIZE, lh: LH, chars: text.length, paragraphs: paragraphs.length, turns: TURNS, margin: MARGIN },
    corpusChars: text.length,
    webview,
    native,
    heapStartMB: heapStart,
    readyMs: round(performance.now() - started),
    ua: navigator.userAgent,
  };

  document.getElementById('result').textContent = JSON.stringify(out, null, 2);
  await fetch('/__ab', { method: 'POST', body: JSON.stringify(out) });
  document.title = 'done';
})().catch(async (err) => {
  console.error(err);
  const info = { error: String(err), stack: String(err.stack) };
  document.getElementById('result').textContent = JSON.stringify(info);
  try {
    await fetch('/__ab', { method: 'POST', body: JSON.stringify(info) });
  } catch (_) {}
  document.title = 'error';
});
