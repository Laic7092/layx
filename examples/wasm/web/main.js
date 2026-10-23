'use strict';

(() => {
  const $ = (sel) => document.querySelector(sel);
  const canvas = $('#page');
  const overlay = $('#overlay');

  const state = {
    ready: false,
    w: 0,
    h: 0,
    dpr: Math.min(window.devicePixelRatio || 1, 2),
    page: 0,
    pages: 0,
    fontSize: 18,
    lineHeight: 1.65,
    justify: true,
    theme: 0,
    layoutMs: 0,
    renderMs: 0,
    sections: [],
    drag: null,
    raf: 0,
    overlayRaf: 0,
    relayoutTimer: 0,
  };

  const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));

  function setStatus(text, isError) {
    const el = $('#status');
    el.textContent = text;
    el.classList.toggle('error', !!isError);
  }

  function rpc(name, ...args) {
    const raw = window.layx[name](...args);
    const out = JSON.parse(raw);
    if (out && out.error) throw new Error(out.error);
    return out;
  }

  function guard(fn) {
    return (...args) => {
      try {
        return fn(...args);
      } catch (err) {
        console.error(err);
        setStatus('错误：' + err.message, true);
      }
    };
  }

  // ------------------------------------------------------------ wasm

  async function waitFor(pred, timeout = 15000) {
    const start = performance.now();
    for (;;) {
      if (pred()) return;
      if (performance.now() - start > timeout) throw new Error('wasm 启动超时');
      await new Promise((r) => setTimeout(r, 20));
    }
  }

  async function startGo() {
    const go = new Go();
    const url = 'layx.wasm';
    const fetchBytes = async () => {
      const resp = await fetch(url);
      if (!resp.ok) throw new Error(`HTTP ${resp.status} for ${url}`);
      return resp;
    };
    let instance;
    if (WebAssembly.instantiateStreaming) {
      try {
        instance = (await WebAssembly.instantiateStreaming(await fetchBytes(), go.importObject)).instance;
      } catch (err) {
        // Streaming compilation is picky about MIME types; retry as a buffer.
        instance = (await WebAssembly.instantiate(await (await fetchBytes()).arrayBuffer(), go.importObject)).instance;
      }
    } else {
      instance = (await WebAssembly.instantiate(await (await fetchBytes()).arrayBuffer(), go.importObject)).instance;
    }
    go.run(instance).catch((err) => setStatus('wasm 运行时错误：' + err, true));
    await waitFor(() => window.layx);
  }

  // ------------------------------------------------------------ layout

  function pageSize() {
    const stage = $('#stage');
    const availW = Math.max(300, stage.clientWidth - 56);
    const availH = Math.max(320, stage.clientHeight - 56 - 38);
    let w = Math.min(720, availW);
    let h = Math.round(w * 1.42);
    if (h > availH) {
      h = Math.round(availH);
      w = Math.min(w, Math.round(h / 1.42));
    }
    return { w: Math.round(w), h };
  }

  function applyCanvasSize(w, h) {
    for (const el of [canvas, overlay]) {
      el.width = Math.round(w * state.dpr);
      el.height = Math.round(h * state.dpr);
      el.style.width = w + 'px';
      el.style.height = h + 'px';
    }
    state.w = w;
    state.h = h;
  }

  const relayout = guard(function relayout(preserveRatio) {
    if (!state.ready) return;
    const ratio = preserveRatio && state.pages > 1 ? state.page / (state.pages - 1) : 0;
    const { w, h } = pageSize();
    applyCanvasSize(w, h);
    const res = rpc('layout', w, h, state.fontSize, state.lineHeight, state.justify, state.theme);
    state.layoutMs = res.ms;
    state.pages = res.pages;
    state.page = res.pages > 1 ? clamp(Math.round(ratio * (res.pages - 1)), 0, res.pages - 1) : 0;
    state.sections = res.sections;
    renderSections();
    updateChrome();
    requestRender();
  });

  const requestRender = guard(function requestRender() {
    if (state.raf) return;
    state.raf = requestAnimationFrame(() => {
      state.raf = 0;
      const res = rpc('render', state.page);
      state.page = res.page;
      state.pages = res.pages;
      state.renderMs = res.ms;
      updateChrome();
      requestOverlay();
    });
  });

  const requestOverlay = guard(function requestOverlay() {
    if (state.overlayRaf) return;
    state.overlayRaf = requestAnimationFrame(() => {
      state.overlayRaf = 0;
      if (!state.ready) return;
      rpc('overlay', state.page);
    });
  });

  function setPage(n) {
    if (!state.ready || state.pages === 0) return;
    state.page = clamp(n, 0, state.pages - 1);
    updateChrome();
    requestRender();
  }

  // ------------------------------------------------------------ chrome

  function updateChrome() {
    $('#pages').textContent = state.pages || '–';
    $('#layoutMs').textContent = state.layoutMs ? state.layoutMs.toFixed(1) : '–';
    $('#renderMs').textContent = state.renderMs ? state.renderMs.toFixed(1) : '–';
    $('#pageLabel').textContent = `${state.pages ? state.page + 1 : 0} / ${state.pages}`;
    $('#btnPrev').disabled = state.page <= 0;
    $('#btnNext').disabled = state.page >= state.pages - 1;

    const slider = $('#pageSlider');
    slider.max = String(Math.max(0, state.pages - 1));
    slider.value = String(state.page);

    let active = -1;
    state.sections.forEach((s, i) => {
      if (s.page <= state.page) active = i;
    });
    document.querySelectorAll('#sections li').forEach((li, i) => {
      li.classList.toggle('active', i === active);
    });
  }

  function renderSections() {
    const list = $('#sections');
    list.textContent = '';
    for (const section of state.sections) {
      const li = document.createElement('li');
      const btn = document.createElement('button');
      btn.type = 'button';
      const title = document.createElement('span');
      title.className = 't';
      title.textContent = section.title;
      const page = document.createElement('span');
      page.className = 'n';
      page.textContent = 'p' + (section.page + 1);
      btn.append(title, page);
      btn.addEventListener('click', () => setPage(section.page));
      li.append(btn);
      list.append(li);
    }
  }

  // ------------------------------------------------------------ pointer

  function canvasPoint(ev) {
    const rect = canvas.getBoundingClientRect();
    return {
      x: (ev.clientX - rect.left) * (state.w / rect.width),
      y: (ev.clientY - rect.top) * (state.h / rect.height),
    };
  }

  const selectRange = guard(function selectRange(x0, y0, x1, y1) {
    const res = rpc('select', x0, y0, x1, y1);
    if (res.caret) {
      setStatus(`命中 · node ${res.a.node} · offset ${res.a.offset} · anchor ${res.a.anchor}`);
    } else {
      setStatus(`选区 · node ${res.a.node}:${res.a.offset} → node ${res.b.node}:${res.b.offset}`);
    }
    requestOverlay();
  });

  canvas.addEventListener('pointerdown', guard((ev) => {
    if (!state.ready) return;
    const p = canvasPoint(ev);
    const link = rpc('linkAt', p.x, p.y);
    if (link.url) {
      window.open(link.url, '_blank', 'noopener');
      return;
    }
    state.drag = { x0: p.x, y0: p.y, moved: false };
    canvas.setPointerCapture(ev.pointerId);
    selectRange(p.x, p.y, p.x, p.y);
    ev.preventDefault();
  }));

  canvas.addEventListener('pointermove', guard((ev) => {
    if (!state.ready) return;
    const p = canvasPoint(ev);
    if (state.drag) {
      if (Math.hypot(p.x - state.drag.x0, p.y - state.drag.y0) > 3) state.drag.moved = true;
      selectRange(state.drag.x0, state.drag.y0, p.x, p.y);
      return;
    }
    const link = rpc('linkAt', p.x, p.y);
    canvas.style.cursor = link.url ? 'pointer' : 'text';
  }));

  const endDrag = () => { state.drag = null; };
  canvas.addEventListener('pointerup', endDrag);
  canvas.addEventListener('pointercancel', endDrag);

  // ------------------------------------------------------------ controls

  $('#btnPrev').addEventListener('click', () => setPage(state.page - 1));
  $('#btnNext').addEventListener('click', () => setPage(state.page + 1));
  $('#pageSlider').addEventListener('input', (ev) => setPage(Number(ev.target.value)));

  $('#fontSize').addEventListener('input', (ev) => {
    state.fontSize = Number(ev.target.value);
    $('#fontSizeVal').textContent = state.fontSize + 'px';
    clearTimeout(state.relayoutTimer);
    state.relayoutTimer = setTimeout(() => relayout(true), 140);
  });

  $('#lineHeight').addEventListener('input', (ev) => {
    state.lineHeight = Number(ev.target.value);
    $('#lineHeightVal').textContent = state.lineHeight.toFixed(2);
    clearTimeout(state.relayoutTimer);
    state.relayoutTimer = setTimeout(() => relayout(true), 140);
  });

  $('#justify').addEventListener('change', (ev) => {
    state.justify = ev.target.checked;
    relayout(true);
  });

  document.querySelectorAll('.themes button').forEach((btn) => {
    btn.addEventListener('click', () => {
      document.querySelectorAll('.themes button').forEach((b) => b.classList.toggle('active', b === btn));
      state.theme = Number(btn.dataset.theme);
      relayout(true);
    });
  });

  document.addEventListener('keydown', (ev) => {
    if (ev.target instanceof HTMLInputElement || ev.target instanceof HTMLTextAreaElement) return;
    switch (ev.key) {
      case 'ArrowLeft':
      case 'PageUp':
        ev.preventDefault();
        setPage(state.page - 1);
        break;
      case 'ArrowRight':
      case 'PageDown':
        ev.preventDefault();
        setPage(state.page + 1);
        break;
      case 'Home':
        ev.preventDefault();
        setPage(0);
        break;
      case 'End':
        ev.preventDefault();
        setPage(state.pages - 1);
        break;
    }
  });

  let resizeTimer = 0;
  window.addEventListener('resize', () => {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(() => relayout(true), 180);
  });

  // ------------------------------------------------------------ boot

  (async () => {
    try {
      setStatus('加载 wasm…');
      await startGo();

      setStatus('加载字体…');
      const resp = await fetch('fonts/book.font');
      if (!resp.ok) throw new Error(`字体加载失败：HTTP ${resp.status}`);
      const fontBytes = new Uint8Array(await resp.arrayBuffer());

      const info = rpc('boot', canvas, overlay, fontBytes, state.dpr);
      $('#fontInfo').textContent = `${info.family} · ${info.faces} faces · UPEM ${info.upem}`;
      state.ready = true;
      relayout(false);
      setStatus('就绪 · 拖动选中文字，或点击查看命中');
    } catch (err) {
      console.error(err);
      setStatus('启动失败：' + err.message, true);
    }
  })();
})();
