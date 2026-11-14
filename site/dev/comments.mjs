// Review comments for the docs, in `just docs-dev` only: select text on a page, comment, and
// it's kept in site/.comments.jsonl (git-ignored) with the page, the passage and the source
// file, for whoever's fixing the docs (`just docs-comments` lists the open ones). The built
// site has none of this. site/README.md says more.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const store = path.join(root, '.comments.jsonl');
const docs = path.join(root, 'src/content/docs');
const route = '/__docs-comments';

// Generated pages are fixed in what they're made from (scripts/docsgen).
const generated = {
	'reference/config': 'internal/hammockconfig/keys.go',
	'reference/agents': 'internal/agents (and scripts/docsgen)',
	'reference/cli': "cmd/hmk/main.go's usage (hmk help)",
	'reference/mcp': 'the tool descriptions in cmd/hmk/mcp*.go',
	'reference/keyboard-shortcuts': 'apple/Sources/HammockKit/Keybinds.swift (and scripts/docsgen)',
};

/** The file a page comes from, relative to the repo, from its path under the base. */
function sourceOf(page, base) {
	let slug = page.startsWith(base) ? page.slice(base.length) : page;
	slug = slug.replace(/^\/+|\/+$/g, '') || 'index';
	if (generated[slug]) return `generated: ${generated[slug]}`;
	for (const ext of ['.md', '.mdx']) {
		const f = path.join(docs, slug + ext);
		if (fs.existsSync(f)) return path.relative(path.join(root, '..'), f);
	}
	return '';
}

function readAll() {
	if (!fs.existsSync(store)) return [];
	return fs
		.readFileSync(store, 'utf8')
		.split('\n')
		.filter((l) => l.trim())
		.flatMap((l) => {
			try {
				return [JSON.parse(l)];
			} catch {
				return [];
			}
		});
}

function writeAll(list) {
	fs.writeFileSync(store, list.map((c) => JSON.stringify(c)).join('\n') + (list.length ? '\n' : ''));
}

function body(req) {
	return new Promise((resolve, reject) => {
		let s = '';
		req.on('data', (d) => (s += d));
		req.on('end', () => {
			try {
				resolve(s ? JSON.parse(s) : {});
			} catch (e) {
				reject(e);
			}
		});
	});
}

function send(res, status, value) {
	res.statusCode = status;
	res.setHeader('Content-Type', 'application/json');
	res.end(JSON.stringify(value));
}

export default function docsComments() {
	return {
		name: 'hammock-docs-comments',
		hooks: {
			'astro:config:setup': ({ command, config, injectScript, updateConfig }) => {
				if (command !== 'dev') return;
				const base = config.base.replace(/\/$/, '') + '/';
				injectScript('page', `import ${JSON.stringify(path.join(root, 'dev/comments-client.js'))};`);
				updateConfig({
					vite: {
						plugins: [
							{
								name: 'hammock-docs-comments-api',
								configureServer(server) {
									server.middlewares.use(route, async (req, res) => {
										try {
											const url = new URL(req.url, 'http://x');
											if (req.method === 'GET') {
												const page = url.searchParams.get('page');
												return send(res, 200, readAll().filter((c) => !page || c.page === page));
											}
											if (req.method === 'POST') {
												const { page, quote, context, comment } = await body(req);
												if (!page || !comment?.trim()) return send(res, 400, { error: 'page and comment are needed' });
												const c = {
													id: Date.now().toString(36) + Math.random().toString(36).slice(2, 6),
													time: new Date().toISOString(),
													status: 'open',
													page,
													source: sourceOf(page, base),
													context: context || '',
													quote: (quote || '').slice(0, 2000),
													comment: comment.trim(),
												};
												fs.appendFileSync(store, JSON.stringify(c) + '\n');
												return send(res, 201, c);
											}
											if (req.method === 'PATCH' || req.method === 'DELETE') {
												const id = url.searchParams.get('id');
												const list = readAll();
												const i = list.findIndex((c) => c.id === id);
												if (i < 0) return send(res, 404, { error: 'no such comment' });
												if (req.method === 'DELETE') list.splice(i, 1);
												else {
													const { status } = await body(req);
													if (status === 'open' || status === 'resolved') list[i].status = status;
												}
												writeAll(list);
												return send(res, 200, { ok: true });
											}
											send(res, 405, { error: 'method' });
										} catch (e) {
											send(res, 500, { error: String(e) });
										}
									});
								},
							},
						],
					},
				});
			},
		},
	};
}
