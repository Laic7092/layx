// Headless snapshot page: renders one page of the demo document onto a bare
// canvas so scripts/snapshot.sh can capture it with a headless browser. All
// parameters come from the query string; there is no UI.
//
//   shot.html?page=1&w=640&h=900&size=18&lh=1.65&theme=0&justify=1&dpr=2
//
// When the frame is ready it sets document.title to "ready <page>/<pages>";
// on failure it sets "error: <message>".
(async () => {
  const q = new URLSearchParams(location.search);
  const num = (k, d) => (q.has(k) ? Number(q.get(k)) : d);
  const w = num('w', 640);
  const h = num('h', 900);
  const size = num('size', 18);
  const lh = num('lh', 1.65);
  const theme = num('theme', 0);
  const dpr = num('dpr', 2);
  const want = Math.max(0, num('page', 1) - 1);
  const justify = q.get('justify') !== '0';

  const canvas = document.getElementById('page');
  const overlay = document.createElement('canvas');
  canvas.style.width = w + 'px';
  canvas.style.height = h + 'px';
  canvas.width = Math.round(w * dpr);
  canvas.height = Math.round(h * dpr);
  overlay.width = canvas.width;
  overlay.height = canvas.height;

  const go = new Go();
  const { instance } = await WebAssembly.instantiateStreaming(fetch('layx.wasm'), go.importObject);
  go.run(instance);
  while (!window.layx) await new Promise((r) => setTimeout(r, 10));

  const call = (name, ...args) => {
    const out = JSON.parse(window.layx[name](...args));
    if (out && out.error) throw new Error(`${name}: ${out.error}`);
    return out;
  };

  const fontResp = await fetch('fonts/book.font');
  if (!fontResp.ok) throw new Error(`font: HTTP ${fontResp.status}`);
  const fontBytes = new Uint8Array(await fontResp.arrayBuffer());

  call('boot', canvas, overlay, fontBytes, dpr);
  const layout = call('layout', w, h, size, lh, justify, theme);
  const rendered = call('render', Math.max(0, Math.min(want, layout.pages - 1)));

  document.body.dataset.page = String(rendered.page + 1);
  document.body.dataset.pages = String(rendered.pages);
  document.body.dataset.layoutMs = layout.ms.toFixed(2);
  document.title = `ready ${rendered.page + 1}/${rendered.pages}`;
})().catch((err) => {
  console.error(err);
  document.title = 'error: ' + err.message;
});
