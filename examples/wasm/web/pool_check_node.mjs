// Headless self-check for the layout-only worker exports.
//
//   ./examples/wasm/build.sh
//   node examples/wasm/web/pool_check_node.mjs
//
// It boots one wasm instance without any canvas, calls initLayout(), then
// compares per-chapter layoutChapter() pages with the same chapters laid out
// in one multi-chapter benchBook() call. Chapters paginate independently, so
// every page count must match exactly (acceptance: ±0 pages).
//
// The actual Web Worker pool is exercised by pool_test.html; this script only
// checks the wasm contract the workers rely on.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const wasmPath = process.argv[2] ?? path.join(here, 'layx.wasm');
const fontPath = process.argv[3] ?? path.join(here, 'fonts/book.font');

for (const p of [wasmPath, fontPath]) {
  if (!fs.existsSync(p)) {
    console.error(`missing ${p}; run ./examples/wasm/build.sh first`);
    process.exit(1);
  }
}

// layout-pool.js publishes globalThis.LayxPool and works as a CommonJS module,
// so the self-check uses the exact same chapter splitter as the browser pool.
await import(pathToFileURL(path.join(here, 'layout-pool.js')).href);
const { splitChapters } = globalThis.LayxPool;

await import(pathToFileURL(path.join(here, 'wasm_exec.js')).href);

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

const PARAS = [
  '这是一段用来检查章节页数确定性的中文小说正文。主角走进屋子，看见桌上摊着一封没有署名的信。窗外下着雨，院子里积水映着灯光，风把门吹得吱呀作响。他站在那里，很久没有说话。',
  '第二章的名字叫“归途”。作者用了不少短句，也用了几个长句，中间夹着 English words 和数字 2024，用来观察中西文混排与自动间距。标点符号、引号、“这样的段落”、以及省略号……都应当被正确处理。',
  '她问他：“你打算怎么办？”他没有立刻回答，而是把信折好，放回信封里。那两个人在门口等了一会儿，最后决定明天一早再出发。雨停了，街上的灯一盏一盏地灭下去。',
  'A paragraph of plain English follows, with some reasonably long words: extraordinary, misunderstanding, characteristically. It exists so the check also covers Latin shaping, justification and line breaking in a mixed document.',
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

const text = corpus(60000);
const chapters = splitChapters(text);
check('splitChapters', chapters.length >= 4, `${chapters.length} chapters, ${text.length} chars`);

const go = new Go();
const { instance } = await WebAssembly.instantiate(fs.readFileSync(wasmPath), go.importObject);
go.run(instance).catch((err) => {
  console.error(err);
  process.exit(1);
});
const deadline = Date.now() + 30000;
while (!globalThis.layx) {
  if (Date.now() > deadline) {
    console.error('wasm boot timeout');
    process.exit(1);
  }
  await new Promise((r) => setTimeout(r, 10));
}

const fontBytes = new Uint8Array(fs.readFileSync(fontPath));
const info = call('initLayout', fontBytes);
check('initLayout without canvas', info.faces > 0, `${info.family}, ${info.faces} face(s), ${info.initMs.toFixed(1)} ms`);

const w = 640;
const h = 900;
const per = chapters.map((chapterText) => call('layoutChapter', chapterText, w, h, 18, 1.65, true));
check(
  'layoutChapter returns pages',
  per.every((r) => r.pages >= 1 && r.ms >= 0),
  per.map((r) => r.pages).join(','),
);

const book = call('benchBook', chapters, w, h, 18, 1.65, true);
check('benchBook pages', book.pages > 0, `${book.pages} pages, ${book.chapters} chapters, ${book.layoutMs.toFixed(1)} ms layout`);

const sum = per.reduce((n, r) => n + r.pages, 0);
check('pool page total == single instance', sum === book.pages, `${sum} vs ${book.pages}`);

const mismatches = [];
for (let i = 0; i < chapters.length; i++) {
  const single = Math.round(book.chapterPages[i]);
  if (per[i].pages !== single) mismatches.push(`ch${i}: ${per[i].pages} vs ${single}`);
}
check('per-chapter pages match single instance', mismatches.length === 0, mismatches.join('; '));

// Re-running a chapter must be stable: the pool relies on chapters being pure
// functions of (text, viewport, typography).
const again = call('layoutChapter', chapters[0], w, h, 18, 1.65, true);
check('layoutChapter deterministic', again.pages === per[0].pages, `${again.pages} pages`);

// A viewport change must actually change pagination, otherwise the page-count
// equality above would be vacuous.
const narrow = call('layoutChapter', chapters[0], 400, h, 18, 1.65, true);
check('layoutChapter responds to viewport', narrow.pages > per[0].pages, `${narrow.pages} vs ${per[0].pages}`);

const single = call('layoutChapter', text, w, h, 18, 1.65, true);
check('whole text as one chapter', single.pages > 0, `${single.pages} pages (chapter splits add page breaks by design)`);

if (failures.length) {
  console.error(`\n${failures.length} failure(s):`);
  for (const f of failures) console.error(' - ' + f);
  process.exit(1);
}
console.log('\nall checks passed');
process.exit(0);
