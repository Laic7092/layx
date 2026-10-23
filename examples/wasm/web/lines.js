// Lay out a corpus the way the A/B webview column does and report every line
// box, so scripts/linecmp.sh can diff them against the engine's line boxes.
//
// Query parameters: w, h (viewport incl. 40px margins), size, lh, trim
// (text-spacing-trim: normal | space-all | ...), and nothing else. The corpus
// is read from /corpus.txt and the font from /fonts/book.font, so both sides of
// the comparison use the same bytes.
(async () => {
  const q = new URLSearchParams(location.search);
  const num = (k, d) => (q.has(k) ? Number(q.get(k)) : d);
  const W = num('w', 640);
  const H = num('h', 900);
  const SIZE = num('size', 18);
  const LH = num('lh', 1.65);
  const MARGIN = 40;
  const CW = W - 2 * MARGIN;
  const CH = H - 2 * MARGIN;
  const GAP = SIZE * 0.5;
  const TRIM = q.get('trim') || '';

  const font = `18px book`;
  await document.fonts.load(font);
  await document.fonts.ready;

  const book = document.getElementById('book');
  book.style.left = MARGIN + 'px';
  book.style.top = MARGIN + 'px';
  book.style.width = CW + 'px';
  book.style.height = CH + 'px';
  book.style.fontSize = SIZE + 'px';
  book.style.lineHeight = String(LH);
  if (TRIM) book.style.textSpacingTrim = TRIM;

  const text = await (await fetch('/corpus.txt?' + Date.now())).text();
  const paragraphs = text.replace(/\n+$/, '').split('\n');
  let html = '';
  for (const p of paragraphs) {
    const d = document.createElement('div');
    d.textContent = p;
    html += '<p>' + d.innerHTML + '</p>';
  }
  book.innerHTML = html;
  if (TRIM) for (const p of book.querySelectorAll('p')) p.style.textSpacingTrim = TRIM;

  // One entry per line box: the text on it and the geometry of its first char.
  const lines = [];
  const perPara = [];
  for (const p of book.querySelectorAll('p')) {
    const node = p.firstChild;
    const before = lines.length;
    if (!node) { perPara.push(0); continue; }
    const s = node.textContent;
    let line = '', top = null, left = 0, right = 0;
    for (let i = 0; i < s.length; i++) {
      const r = document.createRange();
      r.setStart(node, i);
      r.setEnd(node, i + 1);
      const rects = r.getClientRects();
      if (!rects.length) continue;
      const rect = rects[0];
      const t = Math.round(rect.top * 10) / 10;
      if (top === null) {
        top = t;
        left = rect.left;
      } else if (Math.abs(t - top) > 0.5) {
        lines.push({ t: line, w: round(right - left), x: round(left) });
        line = '';
        top = t;
        left = rect.left;
      }
      line += s[i];
      right = rect.right;
    }
    if (line) lines.push({ t: line, w: round(right - left), x: round(left) });
    perPara.push(lines.length - before);
  }

  // Page count of the same content in a multi-column box the size of the page.
  const cols = document.createElement('div');
  cols.id = 'cols';
  cols.style.cssText = `width:${CW}px;height:${CH}px;column-width:${CW}px;` +
    `font-size:${SIZE}px;line-height:${LH};text-align:justify;` +
    `font-family:${getComputedStyle(book).fontFamily};--gap:${GAP}px`;
  if (TRIM) cols.style.textSpacingTrim = TRIM;
  cols.innerHTML = html;
  document.body.appendChild(cols);
  const pageCount = Math.max(1, Math.round(cols.scrollWidth / CW));

  // Does this font draw Latin, digits, CJK, kana and CJK punctuation? A glyph
  // the face lacks falls back to another family, which measures differently
  // from a face that lacks the family outright. Comparing the two catches it.
  const cv = document.createElement('canvas').getContext('2d');
  const measure = (fam, ch) => {
    cv.font = SIZE + 'px ' + fam;
    return Math.round(cv.measureText(ch).width * 100) / 100;
  };
  // Widths cannot tell a full-width CJK glyph from a fallback with the same
  // advance, so compare the drawn pixels instead: the same glyph from the same
  // face hashes the same, a substitute does not.
  const px = document.createElement('canvas');
  px.width = px.height = 64;
  const ctx = px.getContext('2d', { willReadFrequently: true });
  const drawHash = (fam, ch) => {
    ctx.clearRect(0, 0, 64, 64);
    ctx.fillStyle = '#000';
    ctx.font = SIZE + 'px ' + fam;
    ctx.fillText(ch, 4, 48);
    const d = ctx.getImageData(0, 0, 64, 64).data;
    let h = 2166136261;
    for (let i = 3; i < d.length; i += 4) h = ((h ^ d[i]) * 16777619) >>> 0;
    return h;
  };
  const covers = (ch) => drawHash('book', ch) !== drawHash('zz-no-such-family', ch);
  const coverage = {
    latin: covers('A'),
    digit: covers('9'),
    cjk: covers('中'),
    kana: covers('あ'),
    punct: covers('。'),
  };

  // Measure the first line's text again, outside justification, to see the
  // natural width the browser used for it.
  const probeWidth = (txt, trimValue) => {
    const sp = document.createElement('span');
    sp.style.cssText = 'position:absolute;left:-20000px;white-space:nowrap;font-size:' +
      SIZE + 'px;font-family:' + getComputedStyle(book).fontFamily;
    if (trimValue) sp.style.textSpacingTrim = trimValue;
    sp.textContent = txt;
    document.body.appendChild(sp);
    return round(sp.getBoundingClientRect().width);
  };
  const line1 = lines.length ? lines[0].t : '';
  const widths = { normal: probeWidth(line1, 'normal'), spaceAll: probeWidth(line1, 'space-all') };
  // Some builds parse text-spacing-trim yet ignore it while breaking lines
  // (headless Chromium 145 does). Reporting it keeps the space-all control from
  // looking like a real match or a real mismatch.
  const trimProbe = '他说：“你来晚了。”她说：“走吧。”';
  const trimSupported = probeWidth(trimProbe, 'normal') !== probeWidth(trimProbe, 'space-all');
  const pTrim = document.querySelector('#book p') ?
    getComputedStyle(document.querySelector('#book p')).textSpacingTrim : 'none';

  const linesHeight = lines.map((l) => l.t);
  const result = {
    lines,
    count: lines.length,
    perPara,
    pageCount,
    coverage,
    probes: { bookA: measure('book', 'A'), serifA: measure('serif', 'A'), missingA: measure('zz-no-such-family', 'A') },
    page: { w: W, h: H, size: SIZE, lh: LH, cw: CW, ch: CH, trim: TRIM },
    computedTrim: getComputedStyle(book).textSpacingTrim + '/' + pTrim,
    widths,
    trimSupported,
    sample: linesHeight.length,
  };
  await fetch('/__ab', { method: 'POST', body: JSON.stringify(result) });
  document.title = 'done';
})();

function round(v) { return Math.round(v * 100) / 100; }
