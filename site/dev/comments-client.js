// The review-comment widget `just docs-dev` adds to every page (dev/comments.mjs keeps them).
// Select text in a page to comment on it; the Comments button lists the page's comments and
// their replies, and highlights what they're about.
const API = '/__docs-comments';
const page = location.pathname;

const css = `
.hdc-btn, .hdc-badge, .hdc-pop, .hdc-panel { font: 14px/1.45 var(--sl-font, system-ui); color: var(--sl-color-white); z-index: 1000; }
.hdc-btn { position: absolute; padding: 4px 10px; border-radius: 8px; border: 1px solid var(--sl-color-accent);
  background: var(--sl-color-accent); color: var(--sl-color-black); cursor: pointer; font-weight: 600; box-shadow: var(--sl-shadow-md); }
.hdc-badge { position: fixed; right: 16px; bottom: 16px; padding: 8px 14px; border-radius: 999px; cursor: pointer;
  background: var(--sl-color-gray-6); border: 1px solid var(--sl-color-gray-5); box-shadow: var(--sl-shadow-md); font-weight: 600; }
.hdc-badge b { color: var(--sl-color-accent); }
.hdc-pop, .hdc-panel { position: fixed; background: var(--sl-color-gray-6); border: 1px solid var(--sl-color-gray-5);
  border-radius: 12px; box-shadow: var(--sl-shadow-lg); padding: 12px; }
.hdc-pop { width: min(380px, calc(100vw - 32px)); }
.hdc-panel { right: 16px; bottom: 64px; width: min(420px, calc(100vw - 32px)); max-height: 70vh; overflow: auto; }
.hdc-pop textarea { width: 100%; min-height: 90px; resize: vertical; box-sizing: border-box; margin: 8px 0; padding: 8px;
  border-radius: 8px; border: 1px solid var(--sl-color-gray-5); background: var(--sl-color-black); color: var(--sl-color-white); font: inherit; }
.hdc-quote { border-left: 3px solid var(--sl-color-accent); padding-left: 8px; color: var(--sl-color-gray-3);
  max-height: 4.4em; overflow: hidden; cursor: pointer; }
.hdc-row { display: flex; gap: 8px; justify-content: flex-end; align-items: center; }
.hdc-row button { font: inherit; padding: 4px 10px; border-radius: 8px; cursor: pointer; border: 1px solid var(--sl-color-gray-5);
  background: transparent; color: var(--sl-color-white); }
.hdc-row button.primary { background: var(--sl-color-accent); border-color: var(--sl-color-accent); color: var(--sl-color-black); font-weight: 600; }
.hdc-item { padding: 10px 0; border-top: 1px solid var(--sl-color-gray-5); }
.hdc-item:first-of-type { border-top: 0; }
.hdc-item.resolved { opacity: .55; }
.hdc-item p { margin: 6px 0; white-space: pre-wrap; }
.hdc-reply { margin: 6px 0; padding: 6px 8px; border-radius: 8px; background: var(--sl-color-gray-5); white-space: pre-wrap; }
.hdc-meta { font-size: 12px; color: var(--sl-color-gray-3); }
.hdc-hint { font-size: 12px; color: var(--sl-color-gray-3); }
::highlight(hdc) { background-color: rgba(255, 196, 0, .35); }
`;

const el = (tag, cls, text) => {
	const e = document.createElement(tag);
	if (cls) e.className = cls;
	if (text != null) e.textContent = text;
	return e;
};

function content() {
	return document.querySelector('.sl-markdown-content') || document.querySelector('main');
}

let comments = [];
let panel = null;
let pop = null;
const badge = el('button', 'hdc-badge');
const btn = el('button', 'hdc-btn', 'Comment');
btn.hidden = true;

async function load() {
	try {
		const r = await fetch(`${API}?page=${encodeURIComponent(page)}`);
		const next = await r.json();
		if (JSON.stringify(next) !== JSON.stringify(comments)) {
			comments = next;
			render();
		}
	} catch {}
}

function render() {
	const open = comments.filter((c) => c.status === 'open').length;
	badge.innerHTML = '';
	badge.append('Comments ', Object.assign(el('b', '', String(open)), {}));
	if (comments.length > open) badge.append(el('span', 'hdc-meta', ` · ${comments.length - open} resolved`));
	highlight();
	if (panel) renderPanel();
}

// Every text node in the content, so a quote spanning elements can be found.
function rangeFor(quote) {
	const root = content();
	if (!root || !quote) return null;
	const nodes = [];
	let text = '';
	const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
	for (let n; (n = walker.nextNode()); ) {
		nodes.push([n, text.length]);
		text += n.data;
	}
	const norm = (s) => s.replace(/\s+/g, ' ');
	// Match whitespace-insensitively: map positions in the collapsed text back to the original.
	const map = [];
	let flat = '';
	for (let i = 0; i < text.length; i++) {
		if (/\s/.test(text[i]) && /\s$/.test(flat)) continue;
		map.push(i);
		flat += /\s/.test(text[i]) ? ' ' : text[i];
	}
	const q = norm(quote).trim();
	const at = flat.indexOf(q);
	if (at < 0) return null;
	const start = map[at];
	const end = map[at + q.length - 1] + 1;
	const locate = (pos) => {
		for (let i = nodes.length - 1; i >= 0; i--) if (nodes[i][1] <= pos) return [nodes[i][0], pos - nodes[i][1]];
		return null;
	};
	const [sn, so] = locate(start);
	const [en, eo] = locate(end - 1);
	const r = document.createRange();
	r.setStart(sn, so);
	r.setEnd(en, eo + 1);
	return r;
}

function highlight() {
	if (!window.CSS?.highlights) return;
	const ranges = comments.filter((c) => c.status === 'open').map((c) => rangeFor(c.quote)).filter(Boolean);
	CSS.highlights.set('hdc', new Highlight(...ranges));
}

function closePop() {
	pop?.remove();
	pop = null;
}

function headingBefore(node) {
	const hs = [...content().querySelectorAll('h2[id], h3[id], h4[id]')];
	let id = '';
	for (const h of hs) if (h.compareDocumentPosition(node) & Node.DOCUMENT_POSITION_FOLLOWING) id = h.id;
	return id ? `#${id}` : '';
}

function openPop(quote, context, rect) {
	closePop();
	pop = el('div', 'hdc-pop');
	pop.append(el('div', 'hdc-quote', quote));
	const ta = el('textarea');
	ta.placeholder = 'What should change?';
	const save = el('button', 'primary', 'Save');
	const cancel = el('button', '', 'Cancel');
	const row = el('div', 'hdc-row');
	row.append(el('span', 'hdc-hint', '⌘↩ to save'), cancel, save);
	pop.append(ta, row);
	const top = Math.min(rect.bottom + 8, innerHeight - 240);
	pop.style.top = `${Math.max(8, top)}px`;
	pop.style.left = `${Math.max(8, Math.min(rect.left, innerWidth - 400))}px`;
	document.body.append(pop);
	ta.focus();
	const submit = async () => {
		if (!ta.value.trim()) return;
		save.disabled = true;
		await fetch(API, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ page, quote, context, comment: ta.value }),
		});
		closePop();
		getSelection()?.removeAllRanges();
		await load();
	};
	save.onclick = submit;
	cancel.onclick = closePop;
	ta.onkeydown = (e) => {
		if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) submit();
		if (e.key === 'Escape') closePop();
	};
}

function renderPanel() {
	panel.innerHTML = '';
	const head = el('div', 'hdc-row');
	head.style.justifyContent = 'space-between';
	head.append(el('strong', '', 'Comments on this page'), Object.assign(el('button', '', 'Close'), { onclick: togglePanel }));
	panel.append(head);
	if (!comments.length) panel.append(el('p', 'hdc-hint', 'None yet. Select text in the page to comment on it.'));
	const sorted = [...comments].sort((a, b) => (a.status === b.status ? a.time.localeCompare(b.time) : a.status === 'open' ? -1 : 1));
	for (const c of sorted) {
		const item = el('div', `hdc-item ${c.status}`);
		if (c.quote) {
			const q = el('div', 'hdc-quote', c.quote);
			q.title = 'Show in the page';
			q.onclick = () => rangeFor(c.quote)?.startContainer.parentElement?.scrollIntoView({ block: 'center', behavior: 'smooth' });
			item.append(q);
		}
		item.append(el('p', '', c.comment));
		if (c.reply) item.append(el('div', 'hdc-reply', c.reply));
		const row = el('div', 'hdc-row');
		row.style.justifyContent = 'space-between';
		row.append(el('span', 'hdc-meta', `${c.status === 'resolved' ? 'Resolved' : 'Open'} · ${new Date(c.time).toLocaleString()}`));
		const actions = el('span', 'hdc-row');
		const toggle = el('button', '', c.status === 'open' ? 'Resolve' : 'Reopen');
		toggle.onclick = async () => {
			await fetch(`${API}?id=${c.id}`, { method: 'PATCH', body: JSON.stringify({ status: c.status === 'open' ? 'resolved' : 'open' }) });
			load();
		};
		const del = el('button', '', 'Delete');
		del.onclick = async () => {
			await fetch(`${API}?id=${c.id}`, { method: 'DELETE' });
			load();
		};
		actions.append(toggle, del);
		row.append(actions);
		item.append(row);
		panel.append(item);
	}
}

function togglePanel() {
	if (panel) {
		panel.remove();
		panel = null;
		return;
	}
	panel = el('div', 'hdc-panel');
	document.body.append(panel);
	renderPanel();
}

function init() {
	document.head.append(Object.assign(el('style'), { textContent: css }));
	document.body.append(badge, btn);
	badge.onclick = togglePanel;
	btn.onmousedown = (e) => e.preventDefault(); // keep the selection
	btn.onclick = () => {
		const sel = getSelection();
		if (!sel || sel.isCollapsed) return;
		const range = sel.getRangeAt(0);
		btn.hidden = true;
		openPop(sel.toString().trim(), headingBefore(range.startContainer), range.getBoundingClientRect());
	};
	document.addEventListener('selectionchange', () => {
		const sel = getSelection();
		const root = content();
		if (!sel || sel.isCollapsed || !root || !root.contains(sel.anchorNode) || !sel.toString().trim()) {
			btn.hidden = true;
			return;
		}
		const r = sel.getRangeAt(0).getBoundingClientRect();
		btn.style.top = `${r.top + scrollY - 40}px`;
		btn.style.left = `${r.left + scrollX}px`;
		btn.hidden = false;
	});
	load();
	// Replies and fixes arrive while the page is open.
	setInterval(load, 4000);
}

init();
