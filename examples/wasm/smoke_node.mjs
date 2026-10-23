// Headless smoke test for the wasm demo. It stubs out Canvas 2D, boots the
// same wasm module the browser loads, then exercises layout, rendering, hit
// testing, selection and links.
//
//   ./examples/wasm/build.sh
//   node examples/wasm/smoke_node.mjs
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const wasmPath = process.argv[2] ?? path.join(here, 'web/layx.wasm');
const fontPath = process.argv[3] ?? path.join(here, 'web/fonts/book.font');

for (const p of [wasmPath, fontPath]) {
  if (!fs.existsSync(p)) {
    console.error(`missing ${p}; run ./examples/wasm/build.sh first`);
    process.exit(1);
  }
}

const stats = { fills: 0, glyphFills: 0, fillRects: 0, pathChars: 0 };
const ctx = {
  save() {}, restore() {}, beginPath() {}, closePath() {},
  rect() {}, clip() {}, transform() {}, setTransform() {}, clearRect() {},
  moveTo() {}, lineTo() {}, arcTo() {}, stroke() {}, strokeRect() {}, drawImage() {},
  fill(p) {
    stats.fills++;
    if (p && p.d) stats.glyphFills++;
  },
  fillRect() { stats.fillRects++; },
  set fillStyle(v) {}, set strokeStyle(v) {}, set lineWidth(v) {},
};
const canvas = { getContext: () => ctx };
const overlay = { getContext: () => ctx };

globalThis.Path2D = class Path2D {
  constructor(d) {
    this.d = String(d);
    stats.pathChars += this.d.length;
  }
};

await import(pathToFileURL(path.join(here, 'web/wasm_exec.js')).href);

const failures = [];
function check(name, ok, detail = '') {
  if (!ok) failures.push(name + (detail ? ` (${detail})` : ''));
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${name}${detail ? ' — ' + detail : ''}`);
}

function call(name, ...args) {
  const raw = globalThis.layx[name](...args);
  const out = JSON.parse(raw);
  if (out.error) throw new Error(`${name}: ${out.error}`);
  return out;
}

const go = new Go();
const { instance } = await WebAssembly.instantiate(fs.readFileSync(wasmPath), go.importObject);
go.run(instance).catch((err) => { console.error(err); process.exit(1); });

const deadline = Date.now() + 15000;
while (!globalThis.layx) {
  if (Date.now() > deadline) { console.error('wasm boot timeout'); process.exit(1); }
  await new Promise((r) => setTimeout(r, 10));
}

const info = call('boot', canvas, overlay, new Uint8Array(fs.readFileSync(fontPath)), 1);
check('boot', info.faces > 0, `${info.family}, ${info.faces} faces`);

const layout = call('layout', 640, 900, 18, 1.65, true, 0);
check('layout pages', layout.pages >= 2, `${layout.pages} pages, ${layout.ms.toFixed(1)}ms`);
check('sections', layout.sections.length >= 4, `${layout.sections.length} sections`);

let lastSectionPage = -1;
let sectionsOrdered = true;
for (const s of layout.sections) {
  if (s.page < lastSectionPage) sectionsOrdered = false;
  lastSectionPage = s.page;
}
check('section pages ordered', sectionsOrdered);

for (let i = 0; i < layout.pages; i++) {
  const r = call('render', i);
  if (r.page !== i) check(`render page ${i}`, false, `got ${r.page}`);
}
check('render all pages', stats.glyphFills > 0, `${stats.glyphFills} glyph fills, ${stats.pathChars} path chars`);
check('page background', stats.fillRects > 0, `${stats.fillRects} rect fills`);

const hit = call('hitTest', 300, 400);
check('hit test', hit.ok === true, hit.ok ? `node ${hit.hit.node} offset ${hit.hit.offset}` : 'miss');

const sel = call('select', 120, 200, 420, 300);
check('selection', sel.ok && !sel.caret, sel.ok ? `${sel.a.offset}→${sel.b.offset}` : 'failed');
call('overlay', 0);
check('selection overlay', stats.fillRects > 1);
call('clearSelection');

// Scan for the demo link across pages.
let found = '';
outer:
for (let page = 0; page < layout.pages; page++) {
  call('render', page);
  for (let y = 60; y < 880; y += 24) {
    for (let x = 40; x < 620; x += 24) {
      const res = call('linkAt', x, y);
      if (res.url) { found = res.url; break outer; }
    }
  }
}
check('link lookup', found.startsWith('https://'), found || 'not found');

// Relayout under a different size, theme and alignment must still render.
const small = call('layout', 420, 760, 22, 1.9, false, 2);
check('relayout', small.pages >= 1, `${small.pages} pages`);
call('render', small.pages - 1);
check('relayout render', stats.glyphFills > 0);

if (failures.length) {
  console.error(`\n${failures.length} failure(s):`);
  for (const f of failures) console.error(' - ' + f);
  process.exit(1);
}
console.log('\nall checks passed');
process.exit(0);
