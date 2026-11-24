package browserjs

import "fmt"

// ElementAt is what's at a point of the page's viewport, given in fractions
// of it (0...1): a design note's pin (the daemon's pins.go runs it, not an
// agent). It reads the page and changes nothing. Its value, as JSON:
//
//	{url, frameUrl?, w, h (the viewport, CSS px), sx, sy (scrolled), dpr,
//	 x, y (the point, CSS px), el?: {tag, what, selector, text, html, role,
//	 label, rect: {x, y, w, h} (CSS px, in the viewport), styles: {name: value}}}
//
// It looks inside open shadow roots and same-origin frames.
func ElementAt(x, y float64) string {
	return fmt.Sprintf("(%s)(%g,%g)", elementAtJS, x, y)
}

const elementAtJS = `(fx, fy) => {
  const W = innerWidth, H = innerHeight;
  const out = {url: location.href, w: W, h: H, sx: scrollX, sy: scrollY, dpr: devicePixelRatio, x: Math.round(fx * W), y: Math.round(fy * H)};
  let doc = document, ox = 0, oy = 0, frameUrl = '';
  let el = doc.elementFromPoint(fx * W, fy * H);
  for (let i = 0; el && i < 32; i++) {
    const lx = fx * W - ox, ly = fy * H - oy;
    if (el.shadowRoot) {
      const inner = el.shadowRoot.elementFromPoint(lx, ly);
      if (inner && inner !== el) { el = inner; continue; }
    }
    if (el.tagName === 'IFRAME' || el.tagName === 'FRAME') {
      let inner = null;
      try {
        const d = el.contentDocument;
        if (d) {
          const r = el.getBoundingClientRect();
          ox += r.left + el.clientLeft; oy += r.top + el.clientTop;
          doc = d; frameUrl = d.location.href;
          inner = d.elementFromPoint(fx * W - ox, fy * H - oy);
        }
      } catch (e) {}
      if (inner) { el = inner; continue; }
    }
    break;
  }
  if (frameUrl) out.frameUrl = frameUrl;
  if (!el) return out;
  const clip = (s, n) => { s = String(s || '').replace(/\s+/g, ' ').trim(); return s.length > n ? s.slice(0, n) + '…' : s; };
  const esc = (s) => (window.CSS && CSS.escape) ? CSS.escape(s) : String(s).replace(/[^\w-]/g, (c) => '\\' + c);
  // Class names a build made up (CSS modules, styled-components, Emotion)
  // change from one build to the next: not worth selecting by.
  const madeUp = (c) => /^(css|sc|jsx|emotion|svelte)-|[_-][a-zA-Z0-9]{5,}$|\d{3,}|^_/.test(c) && !/^[a-z]+(-[a-z]+)*$/.test(c);
  const root = el.getRootNode ? el.getRootNode() : doc;
  const unique = (sel) => { try { return root.querySelectorAll(sel).length === 1; } catch (e) { return false; } };
  const testAttr = (n) => { for (const a of ['data-testid', 'data-test-id', 'data-test', 'data-cy', 'data-qa']) { const v = n.getAttribute && n.getAttribute(a); if (v) return '[' + a + '="' + v.replace(/"/g, '\\"') + '"]'; } return ''; };
  const step = (n) => {
    const tag = n.tagName.toLowerCase();
    const t = testAttr(n);
    if (t) return {s: tag + t, anchor: true};
    if (n.id && !/\d{4,}|^[:_]|[:]/.test(n.id)) return {s: '#' + esc(n.id), anchor: true};
    let s = tag;
    const cls = [...(n.classList || [])].filter((c) => !madeUp(c)).slice(0, 2);
    if (cls.length) s += cls.map((c) => '.' + esc(c)).join('');
    const name = n.getAttribute && n.getAttribute('name');
    if (name && /^(input|select|textarea|button|form)$/.test(tag)) s += '[name="' + name.replace(/"/g, '\\"') + '"]';
    const p = n.parentElement;
    if (p) {
      const same = [...p.children].filter((c) => c.tagName === n.tagName);
      if (same.length > 1 && !unique(s)) s += ':nth-of-type(' + (same.indexOf(n) + 1) + ')';
    }
    return {s, anchor: false};
  };
  const parts = [];
  for (let n = el, i = 0; n && n.nodeType === 1 && i < 8; n = n.parentElement, i++) {
    const st = step(n);
    parts.unshift(st.s);
    const sel = parts.join(' > ');
    if (st.anchor || unique(sel)) break;
  }
  const tag = el.tagName.toLowerCase();
  const r = el.getBoundingClientRect();
  const text = clip(el.innerText || el.textContent || el.value || el.getAttribute('alt') || '', 160);
  const label = clip(el.getAttribute('aria-label') || el.getAttribute('title') || el.getAttribute('placeholder') || el.getAttribute('alt') || '', 80);
  const role = el.getAttribute('role') || '';
  let what = tag;
  if (el.id) what += '#' + el.id;
  else if (el.classList && el.classList.length) what += '.' + [...el.classList].filter((c) => !madeUp(c)).slice(0, 2).join('.');
  what = what.replace(/\.$/, '');
  const shown = label || clip(text, 40);
  if (shown) what += ' “' + clip(shown, 40) + '”';
  // Its HTML: all of it when short, else its opening tag (each attribute
  // clipped) and how much is inside.
  let html = el.outerHTML || '';
  if (html.length > 600) {
    const attrs = [...el.attributes].map((a) => ' ' + a.name + (a.value === '' ? '' : '="' + clip(a.value, 120) + '"')).join('');
    html = '<' + tag + attrs + '>…' + (el.children.length ? ' (' + el.children.length + ' child elements)' : '') + '…</' + tag + '>';
  }
  const cs = getComputedStyle(el);
  const keys = ['display', 'position', 'width', 'height', 'margin', 'padding', 'color', 'background-color', 'background-image',
    'font-family', 'font-size', 'font-weight', 'line-height', 'letter-spacing', 'text-align', 'text-transform', 'border', 'border-radius',
    'box-shadow', 'opacity', 'gap', 'flex-direction', 'justify-content', 'align-items', 'grid-template-columns', 'z-index', 'overflow'];
  const dull = new Set(['none', 'normal', 'auto', '0px', 'rgba(0, 0, 0, 0)', 'start', 'visible', 'static', '1', '0px none rgb(0, 0, 0)']);
  const styles = {};
  for (const k of keys) {
    const v = cs.getPropertyValue(k);
    if (!v || dull.has(v)) continue;
    if ((k === 'flex-direction' || k === 'justify-content' || k === 'align-items' || k === 'gap') && !/flex|grid/.test(cs.display)) continue;
    if (k === 'flex-direction' && v === 'row') continue;
    if (k === 'border' && /^0px/.test(v)) continue;
    styles[k] = clip(v, 160);
  }
  out.el = {tag, what, selector: parts.join(' > '), text, html, role, label,
    rect: {x: Math.round(r.left + ox), y: Math.round(r.top + oy), w: Math.round(r.width), h: Math.round(r.height)}, styles};
  return out;
}`
