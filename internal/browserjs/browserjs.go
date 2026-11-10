// Package browserjs holds the scripts `hmk mcp` runs in browser panes only
// to read them (browser_snapshot, browser_wait_for, browser_screenshot),
// so the daemon can tell them from any other JavaScript: these run in any
// browser pane an agent may read, anything else only in panes it may
// control (browser_evaluate in your logged-in page in another Group asks).
package browserjs

import (
	"encoding/json"
	"strings"
)

// Snapshot outlines the visible page (snapshotJS), as browser_snapshot
// evaluates it.
const Snapshot = "(" + snapshotJS + ")()"

// DevicePixelRatio is the page's, for browser_screenshot.
const DevicePixelRatio = "window.devicePixelRatio"

const waitForJS = "((text, sel) => sel ? !!document.querySelector(sel) : (document.body ? document.body.innerText : '').includes(text))(..."

// WaitFor is browser_wait_for's check: whether text, or an element matching
// the CSS selector sel, is on the page.
func WaitFor(text, sel string) string {
	args, _ := json.Marshal([]any{text, sel})
	return waitForJS + string(args) + ")"
}

// ReadOnly reports whether a Runtime.evaluate call (its params, as JSON)
// only runs one of the scripts above.
func ReadOnly(paramsJSON string) bool {
	_, ok := ReadOnlyParams(paramsJSON)
	return ok
}

// evalParams are the Runtime.evaluate params a read-only call may have.
type evalParams struct {
	Expression    string `json:"expression"`
	ReturnByValue *bool  `json:"returnByValue,omitempty"`
	AwaitPromise  *bool  `json:"awaitPromise,omitempty"`
	UserGesture   *bool  `json:"userGesture,omitempty"`
}

// ReadOnlyParams is ReadOnly, also giving the params to send in their place:
// re-encoded from what was checked, so the browser runs exactly that. (Sent
// as they came, `{"expression":"evil()","expression":"window.devicePixelRatio"}`
// passes here, Go keeping the last key, and may run the first in Chromium;
// a key repeated is refused too.)
func ReadOnlyParams(paramsJSON string) (string, bool) {
	if dupKeys(paramsJSON) {
		return "", false
	}
	var p map[string]json.RawMessage
	if json.Unmarshal([]byte(paramsJSON), &p) != nil {
		return "", false
	}
	for k := range p {
		switch k {
		case "expression", "returnByValue", "awaitPromise", "userGesture":
		default:
			return "", false // (a context id, the command line API, …)
		}
	}
	var e evalParams
	if json.Unmarshal(p["expression"], &e.Expression) != nil {
		return "", false
	}
	for k, dst := range map[string]**bool{"returnByValue": &e.ReturnByValue, "awaitPromise": &e.AwaitPromise, "userGesture": &e.UserGesture} {
		if raw, ok := p[k]; ok {
			var b bool
			if json.Unmarshal(raw, &b) != nil {
				return "", false
			}
			*dst = &b
		}
	}
	if !readOnlyExpr(e.Expression) {
		return "", false
	}
	var out strings.Builder
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if enc.Encode(e) != nil {
		return "", false
	}
	return strings.TrimSuffix(out.String(), "\n"), true
}

// dupKeys reports whether the JSON object s names a key twice (as decoded:
// "\u0065xpression" is "expression"), or isn't an object.
func dupKeys(s string) bool {
	dec := json.NewDecoder(strings.NewReader(s))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return true
	}
	seen := map[string]bool{}
	for dec.More() {
		t, err := dec.Token()
		k, ok := t.(string)
		if err != nil || !ok || seen[k] {
			return true
		}
		seen[k] = true
		var skip json.RawMessage
		if dec.Decode(&skip) != nil {
			return true
		}
	}
	return false
}

func readOnlyExpr(expr string) bool {
	switch expr {
	case Snapshot, DevicePixelRatio:
		return true
	}
	rest, ok := strings.CutPrefix(expr, waitForJS)
	if !ok {
		return false
	}
	rest, ok = strings.CutSuffix(rest, ")")
	var args []string
	if !ok || json.Unmarshal([]byte(rest), &args) != nil || len(args) != 2 {
		return false
	}
	return expr == WaitFor(args[0], args[1]) // nothing but the two strings
}

// snapshotJS outlines the visible page: text, headings, images and the
// controls an agent can act on, numbered for browser_click/browser_type.
const snapshotJS = `() => {
  const els = window.__hammockEls = [];
  const out = [];
  const max = 40000;
  let size = 0;
  const actionable = new Set(['link','button','textbox','searchbox','combobox','listbox','checkbox','radio','switch','slider',
    'tab','menuitem','menuitemcheckbox','menuitemradio','option','treeitem','spinbutton']);
  const controls = 'a[href],button,input,select,textarea,summary,[role],[contenteditable="true"],[onclick],iframe';
  const clean = s => (s || '').replace(/\s+/g, ' ').trim();
  const visible = el => {
    const s = getComputedStyle(el);
    if (s.display === 'none' || s.visibility === 'hidden' || s.opacity === '0') return false;
    if (s.display === 'contents') return true;
    const r = el.getBoundingClientRect();
    return r.width > 0 || r.height > 0;
  };
  const role = el => {
    const r = el.getAttribute('role');
    if (r) return r;
    const t = el.tagName.toLowerCase();
    if (t === 'a') return el.hasAttribute('href') ? 'link' : null;
    if (t === 'button' || t === 'summary') return 'button';
    if (t === 'select') return 'combobox';
    if (t === 'textarea') return 'textbox';
    if (t === 'input') {
      const ty = (el.type || 'text').toLowerCase();
      if (ty === 'hidden') return null;
      if (['button', 'submit', 'reset', 'image'].includes(ty)) return 'button';
      if (ty === 'checkbox' || ty === 'radio') return ty;
      if (ty === 'range') return 'slider';
      if (ty === 'search') return 'searchbox';
      return 'textbox';
    }
    if (/^h[1-6]$/.test(t)) return 'heading';
    if (t === 'img') return 'img';
    if (el.isContentEditable && el.getAttribute('contenteditable') === 'true') return 'textbox';
    if (el.hasAttribute('onclick')) return 'button';
    return null;
  };
  const name = el => {
    const lab = el.labels && el.labels[0] ? el.labels[0].innerText : '';
    return clean(el.getAttribute('aria-label') || el.getAttribute('alt') || lab || el.getAttribute('title') ||
      (el.tagName === 'INPUT' || el.tagName === 'SELECT' || el.tagName === 'TEXTAREA' ? el.getAttribute('placeholder') : el.innerText) ||
      el.getAttribute('placeholder') || '').slice(0, 120);
  };
  const emit = (depth, line) => {
    if (size > max || !line) return;
    line = '  '.repeat(Math.min(depth, 12)) + line;
    size += line.length + 1;
    out.push(line);
  };
  const walk = (node, depth) => {
    const kids = node.shadowRoot ? [...node.shadowRoot.childNodes, ...node.childNodes] : [...node.childNodes];
    for (const ch of kids) {
      if (size > max) return;
      if (ch.nodeType === 3) {
        const t = clean(ch.textContent);
        if (t) emit(depth, t.slice(0, 1000));
        continue;
      }
      if (ch.nodeType !== 1) continue;
      const el = ch, tag = el.tagName.toUpperCase();
      if (['SCRIPT', 'STYLE', 'NOSCRIPT', 'TEMPLATE', 'SVG', 'HEAD', 'META', 'LINK'].includes(tag)) continue;
      if (!visible(el)) continue;
      if (tag === 'IFRAME') {
        try { if (el.contentDocument && el.contentDocument.body) walk(el.contentDocument.body, depth + 1); } catch (e) {}
        continue;
      }
      const r = role(el);
      if (r && (actionable.has(r) || r === 'heading' || r === 'img')) {
        let line = r + (r === 'heading' && /^H[1-6]$/.test(tag) ? ' ' + tag.toLowerCase() : '') + ' "' + name(el) + '"';
        if (actionable.has(r)) {
          els.push(el);
          line += ' [ref=' + els.length + ']';
        }
        if (r === 'textbox' || r === 'searchbox' || r === 'combobox') {
          const v = el.tagName === 'SELECT' ? (el.selectedOptions[0] || {}).text : ('value' in el ? el.value : '');
          if (v) line += ' value="' + clean(String(v)).slice(0, 200) + '"';
          if (el.tagName === 'SELECT') line += ' options: ' + [...el.options].slice(0, 30).map(o => clean(o.text)).join(' | ');
        }
        if (el.checked || el.getAttribute('aria-checked') === 'true' || el.getAttribute('aria-selected') === 'true') line += ' (checked)';
        if (el.disabled || el.getAttribute('aria-disabled') === 'true') line += ' (disabled)';
        if (el.getAttribute('aria-expanded')) line += ' (expanded=' + el.getAttribute('aria-expanded') + ')';
        if (r === 'link') {
          const h = el.getAttribute('href');
          if (h && !h.startsWith('javascript:') && h !== '#') line += ' -> ' + h.slice(0, 200);
        }
        emit(depth, line);
        // A control's own text is its name; look inside only containers.
        if (['listbox', 'tab', 'menuitem', 'treeitem'].includes(r) && el.querySelector(controls)) walk(el, depth + 1);
        continue;
      }
      // A block of plain text (no controls inside): one line.
      if (!el.querySelector(controls) && !el.shadowRoot) {
        const t = clean(el.innerText);
        if (t) emit(depth, t.slice(0, 2000));
        continue;
      }
      walk(el, depth + (r ? 1 : 0));
    }
  };
  walk(document.body || document.documentElement, 0);
  return {title: document.title, url: location.href, text: out.join('\n'), truncated: size > max};
}`
