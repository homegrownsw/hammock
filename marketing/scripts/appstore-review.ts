// Writes one page for reviewing the whole App Store listing (marketing/appstore/README.md, "Reviewing it"): every
// locale's text with its character counts against App Store Connect's limits, What's New, App Review's notes,
// TestFlight's text, the categories, age rating and privacy answers, and every generated asset (the screenshots,
// the marketing images, the app preview and search results videos with their poster frame, the product page
// header, the contact sheets), grouped by device and appearance. It reads only the files on disk and flags what's
// missing or stale, by the same rules the push refuses them on, plus a few softer ones (older than what made them).
//
//   just appstore-review            # marketing/appstore-review.html (gitignored)
//   just appstore-review --open     # …and open it: in a Hammock browser tab inside a pane, else the default browser
//
// --appstore DIR reads another marketing/appstore (test data); --out FILE writes the page elsewhere.
import { execFileSync, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, readdirSync, statSync, writeFileSync } from "node:fs";
import { dirname, join, relative, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const ROOT = fileURLToPath(new URL("../../", import.meta.url));
let APPSTORE = join(ROOT, "marketing/appstore");
let OUT = join(ROOT, "marketing/appstore-review.html");
let open = false;
const argv = process.argv.slice(2);
for (let i = 0; i < argv.length; i++) {
  if (argv[i] === "--appstore") APPSTORE = resolve(argv[++i] ?? "");
  else if (argv[i] === "--out") OUT = resolve(argv[++i] ?? "");
  else if (argv[i] === "--open") open = true;
  else if (argv[i] === "-h" || argv[i] === "--help") {
    console.log("usage: appstore-review [--open] [--appstore DIR] [--out FILE]");
    process.exit(0);
  } else throw new Error(`unknown argument ${argv[i]}`);
}
const METADATA = join(APPSTORE, "metadata");
const CAPTURE_LOCALE = "en-GB";

// --- Reading the tree.
const read = (p: string) => readFileSync(p, "utf8").replace(/\n$/, "");
const readIf = (p: string) => (existsSync(p) ? read(p) : null);
const json = (p: string) => JSON.parse(readFileSync(p, "utf8"));
const mtime = (p: string) => statSync(p).mtimeMs;
const dirs = (p: string) => (existsSync(p) ? readdirSync(p).filter((n) => statSync(join(p, n)).isDirectory()).sort() : []);
/** Characters as App Store Connect counts them: code points. */
const chars = (s: string) => [...s].length;
const md5 = (p: string) => createHash("md5").update(readFileSync(p)).digest("hex");
const sha256 = (p: string) => createHash("sha256").update(readFileSync(p)).digest("hex");
const git = (...a: string[]) => {
  const r = spawnSync("git", a, { cwd: ROOT, encoding: "utf8" });
  return r.status === 0 ? r.stdout.trim() : null;
};
const stampOf = (p: string) => {
  const t = readIf(p);
  if (t === null) return null;
  return Object.fromEntries(t.split("\n").filter((l) => l.includes("=")).map((l) => [l.slice(0, l.indexOf("=")), l.slice(l.indexOf("=") + 1)]));
};
/** A PNG's size, from its IHDR chunk. */
function pngSize(p: string): { w: number; h: number } | null {
  try {
    const b = readFileSync(p).subarray(0, 24);
    if (b.toString("latin1", 12, 16) !== "IHDR") return null;
    return { w: b.readUInt32BE(16), h: b.readUInt32BE(20) };
  } catch { return null; }
}
interface Probe { w: number; h: number; fps: number; seconds: number; codec: string; audio: string | null; bytes: number }
function probe(p: string): Probe | null {
  const r = spawnSync("ffprobe", ["-v", "error", "-print_format", "json", "-show_streams", "-show_format", p], { encoding: "utf8" });
  if (r.status !== 0) return null;
  const j = JSON.parse(r.stdout);
  const v = j.streams.find((s: any) => s.codec_type === "video");
  const a = j.streams.find((s: any) => s.codec_type === "audio");
  if (!v) return null;
  const [n, d] = String(v.avg_frame_rate || v.r_frame_rate || "0/1").split("/").map(Number);
  return { w: v.width, h: v.height, fps: d ? n / d : 0, seconds: Number(j.format.duration) || 0, codec: v.codec_name,
    audio: a ? a.codec_name : null, bytes: Number(j.format.size) || 0 };
}

const iosVersion = readIf(join(ROOT, "apple/iOS/Config/Version.xcconfig"))?.match(/^HAMMOCK_IOS_VERSION = (\S+)/m)?.[1] ?? "?";
const head = git("rev-parse", "--short", "HEAD") ?? "?";
const branch = git("rev-parse", "--abbrev-ref", "HEAD") ?? "?";
const inWorktree = ROOT.includes(`${sep}.claude${sep}worktrees${sep}`);
const locales = dirs(METADATA).filter((n) => !n.startsWith("review_") && !n.startsWith("trade_"));
const scenes: { id: string; what: string }[] = json(join(APPSTORE, "scenes.json")).scenes;
const mk = json(join(APPSTORE, "marketing.json")) as {
  panels: { id: string; shot: string; theme: "light" | "dark"; headline: string; subline: string }[];
  header: { wordmark: string; tagline: string };
};
const edit = json(join(APPSTORE, "preview.json")).edit as {
  poster: number;
  shots: { take: string; mark: string; from: number; seconds: number; caption: string }[];
  end: { seconds: number; headline: string; subline: string };
};

// --- Flags: what's wrong, where, and the command that fixes it.
type Level = "error" | "warn";
interface Flag { level: Level; section: string; text: string; fix?: string }
const flags: Flag[] = [];
const flag = (level: Level, section: string, text: string, fix?: string) => {
  flags.push({ level, section, text, fix });
  return { level, text };
};

// --- HTML helpers.
const esc = (s: string) => s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]!);
/** A file's URL from the page, with its mtime so a regenerated file isn't served from cache. */
const url = (p: string) =>
  relative(dirname(OUT), p).split(sep).map(encodeURIComponent).join("/") + (existsSync(p) ? `?v=${Math.round(mtime(p))}` : "");
/** A path as the repo names it: marketing/appstore/… even for test data read from elsewhere. */
const shown = (p: string) => {
  const r = relative(APPSTORE, p);
  return !r.startsWith("..") ? join("marketing/appstore", r) : relative(ROOT, p) || p;
};
const plural = (n: number, w: string) => `${n} ${w}${n === 1 ? "" : "s"}`;
const ago = (ms: number) => {
  const s = (Date.now() - ms) / 1000;
  if (s < 90) return "just now";
  if (s < 5400) return `${Math.round(s / 60)} min ago`;
  if (s < 129600) return `${Math.round(s / 3600)} h ago`;
  return `${Math.round(s / 86400)} days ago`;
};
const fmtBytes = (n: number) => (n > 1e6 ? `${(n / 1e6).toFixed(1)} MB` : `${Math.round(n / 1e3)} KB`);
const badge = (cls: string, text: string, title = "") => `<span class="badge ${cls}"${title ? ` title="${esc(title)}"` : ""}>${esc(text)}</span>`;
const flagBadges = (fs: { level: Level; text: string }[]) =>
  fs.map((f) => badge(f.level, f.level === "error" ? (f.text.startsWith("missing") ? "Missing" : "Problem") : "Stale", f.text)).join("");
/** *starred* words in Hammock's colours, \n as line breaks: marketing.json's and preview.json's copy. */
const copyHTML = (s: string) => esc(s).replace(/\*([^*]+)\*/g, "<em>$1</em>").replace(/\n/g, "<br>");
function meter(n: number, limit: number) {
  const pct = Math.min(100, (n / limit) * 100);
  const cls = n > limit ? "over" : n > limit * 0.9 ? "near" : "ok";
  return `<span class="count ${cls}"><span class="bar"><i style="width:${pct.toFixed(1)}%"></i></span><b>${n}</b>/${limit}</span>`;
}

/** Just enough Markdown for app-privacy.md: headings, paragraphs, lists, tables, bold, code, links. */
function markdown(src: string): string {
  const inline = (s: string) => esc(s)
    .replace(/`([^`]+)`/g, "<code>$1</code>")
    .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
    .replace(/\*([^*]+)\*/g, "<em>$1</em>")
    .replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2">$1</a>');
  const out: string[] = [];
  const lines = src.split("\n");
  for (let i = 0; i < lines.length;) {
    const l = lines[i];
    if (!l.trim()) { i++; continue; }
    const h = l.match(/^(#{1,4})\s+(.*)/);
    if (h) { const n = Math.min(4, h[1].length + 2); out.push(`<h${n}>${inline(h[2])}</h${n}>`); i++; continue; }
    if (l.startsWith("|")) {
      const rows: string[][] = [];
      while (i < lines.length && lines[i].startsWith("|")) {
        if (!/^\|[\s:|-]+\|$/.test(lines[i])) rows.push(lines[i].slice(1, -1).split("|").map((c) => c.trim()));
        i++;
      }
      const [th, ...td] = rows;
      out.push(`<table><thead><tr>${th.map((c) => `<th>${inline(c)}</th>`).join("")}</tr></thead><tbody>${
        td.map((r) => `<tr>${r.map((c) => `<td>${inline(c)}</td>`).join("")}</tr>`).join("")}</tbody></table>`);
      continue;
    }
    if (/^[-*] /.test(l)) {
      const items: string[] = [];
      while (i < lines.length && (/^[-*] /.test(lines[i]) || /^\s+\S/.test(lines[i]))) {
        if (/^[-*] /.test(lines[i])) items.push(lines[i].slice(2)); else items[items.length - 1] += " " + lines[i].trim();
        i++;
      }
      out.push(`<ul>${items.map((t) => `<li>${inline(t)}</li>`).join("")}</ul>`);
      continue;
    }
    const para: string[] = [];
    while (i < lines.length && lines[i].trim() && !/^(#|\||[-*] )/.test(lines[i])) para.push(lines[i++]);
    out.push(`<p>${inline(para.join(" "))}</p>`);
  }
  return out.join("\n");
}

// ============================================================================================================
// The listing text.
const FIELDS: { file: string; label: string; limit?: number; kind?: "url" | "keywords" | "long" }[] = [
  { file: "name.txt", label: "Name", limit: 30 },
  { file: "subtitle.txt", label: "Subtitle", limit: 30 },
  { file: "promotional_text.txt", label: "Promotional text", limit: 170 },
  { file: "description.txt", label: "Description", limit: 4000, kind: "long" },
  { file: "keywords.txt", label: "Keywords", limit: 100, kind: "keywords" },
  { file: "support_url.txt", label: "Support URL", limit: 255, kind: "url" },
  { file: "marketing_url.txt", label: "Marketing URL", limit: 255, kind: "url" },
  { file: "privacy_url.txt", label: "Privacy policy URL", limit: 255, kind: "url" },
];

function fieldCard(locale: string, f: (typeof FIELDS)[number], base: string | null): string {
  const path = join(METADATA, locale, f.file);
  const text = readIf(path);
  const fs: { level: Level; text: string }[] = [];
  if (text === null) {
    if (["name.txt", "description.txt", "keywords.txt", "support_url.txt", "privacy_url.txt"].includes(f.file)) {
      fs.push(flag("error", "listing", `missing: metadata/${locale}/${f.file}`));
    }
    return `<div class="field empty"><div class="field-head"><h4>${f.label}</h4>${flagBadges(fs)}<span class="muted">not set</span></div></div>`;
  }
  const n = chars(text);
  if (f.limit && n > f.limit) fs.push(flag("error", "listing", `metadata/${locale}/${f.file} is ${n} characters, over ${f.limit}`));
  if (text.includes("[OWNER")) fs.push(flag("warn", "listing", `metadata/${locale}/${f.file} has an [OWNER …] placeholder`));
  const same = base !== null && base === text ? `<span class="same" title="Identical to ${CAPTURE_LOCALE}">= ${CAPTURE_LOCALE}</span>` : "";
  let body: string;
  if (f.kind === "url") body = `<a class="url" href="${esc(text)}" target="_blank" rel="noopener">${esc(text)}</a>`;
  else if (f.kind === "keywords") {
    const words = text.split(",").map((w) => w.trim()).filter(Boolean);
    body = `<div class="chips">${words.map((w) => `<span class="chip">${esc(w)}<small>${chars(w)}</small></span>`).join("")}</div>
      <p class="muted small">${plural(words.length, "keyword")}, comma-separated, ${100 - n} characters to spare.</p>`;
  } else if (f.kind === "long") body = `<div class="prose pre">${esc(text)}</div>`;
  else body = `<div class="value">${esc(text)}</div>`;
  return `<div class="field${f.kind === "long" ? " long" : ""}"><div class="field-head"><h4>${f.label}</h4>${same}${flagBadges(fs)}${f.limit ? meter(n, f.limit) : ""}</div>${body}</div>`;
}

function storeCard(locale: string): string {
  const name = readIf(join(METADATA, locale, "name.txt")) ?? "";
  const sub = readIf(join(METADATA, locale, "subtitle.txt")) ?? "";
  const promo = readIf(join(METADATA, locale, "promotional_text.txt")) ?? "";
  return `<div class="store">
    <div class="store-row"><span class="icon big"><img src="${url(ICON)}" alt=""></span>
      <div><div class="store-name">${esc(name)}</div><div class="store-sub">${esc(sub)}</div><span class="get">Get</span></div></div>
    <p class="store-promo">${esc(promo)}</p></div>`;
}
const ICON = join(ROOT, "apple/iOS/Hammock/AppIcon.icon/Assets/mark.png");

const baseTexts = Object.fromEntries(FIELDS.map((f) => [f.file, readIf(join(METADATA, CAPTURE_LOCALE, f.file))]));
const listingTabs = locales.map((loc) => {
  const differs = FIELDS.filter((f) => loc !== CAPTURE_LOCALE && readIf(join(METADATA, loc, f.file)) !== baseTexts[f.file]);
  const note = loc === CAPTURE_LOCALE ? "the primary language" : differs.length ? `${plural(differs.length, "field")} differ from ${CAPTURE_LOCALE}` : `identical to ${CAPTURE_LOCALE}`;
  return {
    id: loc, note,
    html: `<div class="listing">${storeCard(loc)}<div class="fields">${FIELDS.map((f) => fieldCard(loc, f, loc === CAPTURE_LOCALE ? null : baseTexts[f.file])).join("")}</div></div>`,
  };
});

// What's New: from the release notes, for the iPhone version the project builds (with the notes not cut yet), as
// the push sends it. The version App Store Connect is editing could be another; the preview says which.
const whatsNewScript = join(ROOT, "marketing/scripts/appstore-whats-new");
const wn = spawnSync(whatsNewScript, [iosVersion, "--pending"], { cwd: ROOT, encoding: "utf8" });
const whatsNew = wn.status === 0 ? wn.stdout.replace(/\n+$/, "") : null;
if (whatsNew === null) flag("error", "whats-new", `appstore-whats-new ${iosVersion} --pending failed: ${(wn.stderr || "").trim().split("\n").pop()}`);
else if (chars(whatsNew) > 4000) flag("error", "whats-new", `What's New is ${chars(whatsNew)} characters, over 4000`);

const reviewNotes = readIf(join(METADATA, "review_information/notes.txt"));
if (reviewNotes === null) flag("error", "review", "missing: metadata/review_information/notes.txt");
else if (chars(reviewNotes) > 4000) flag("error", "review", `App Review's notes are ${chars(reviewNotes)} characters, over 4000`);

const tfLocales = dirs(join(APPSTORE, "testflight"));

const CATEGORY_NAMES: Record<string, string> = { DEVELOPER_TOOLS: "Developer Tools", PRODUCTIVITY: "Productivity", UTILITIES: "Utilities", BUSINESS: "Business" };
const appInfo = {
  primary: readIf(join(METADATA, "primary_category.txt")),
  secondary: readIf(join(METADATA, "secondary_category.txt")),
  copyright: readIf(join(METADATA, "copyright.txt")),
};

// The offline check the push runs first, so its verdict is on the page too.
const check = spawnSync("node", ["--disable-warning=ExperimentalWarning", join(ROOT, "marketing/scripts/check-metadata.ts")], { cwd: ROOT, encoding: "utf8" });
const checkLines = `${check.stdout}${check.stderr}`.trim().split("\n").filter(Boolean);
const checkOK = check.status === 0 && APPSTORE === join(ROOT, "marketing/appstore");

// ============================================================================================================
// The generated assets.
type DeviceId = "iphone" | "ipad" | "duo";
const DEVICES: { id: DeviceId; name: string; tag: string; w: number; h: number; shots: string; out: string; alt?: [number, number] }[] = [
  { id: "iphone", name: '6.9" iPhone', tag: "iphone69", w: 1320, h: 2868, shots: "screenshots", out: "marketing" },
  { id: "ipad", name: '13" iPad', tag: "ipad13", w: 2064, h: 2752, shots: "screenshots", out: "marketing" },
  { id: "duo", name: "iPhone Duo (unfolded)", tag: "duo", w: 2853, h: 2007, shots: "duo/screenshots", out: "duo/marketing", alt: [2007, 2853] },
];
const APPEARANCES = ["light", "dark"] as const;
const shotDir = (d: (typeof DEVICES)[number], a: "light" | "dark") => join(APPSTORE, a === "dark" ? `${d.shots}-dark` : d.shots, CAPTURE_LOCALE);

interface Tile { file: string; title: string; sub: string; w: number; h: number; flags: { level: Level; text: string }[]; fix: string; group: string }
function sizeCheck(section: string, p: string, w: number, h: number, alt?: [number, number]) {
  const s = pngSize(p);
  if (!s) return { s, f: [flag("error", section, `${shown(p)} isn't a readable PNG`)] };
  const ok = (s.w === w && s.h === h) || (alt && s.w === alt[0] && s.h === alt[1]);
  return { s, f: ok ? [] : [flag("error", section, `${shown(p)} is ${s.w} × ${s.h}, not ${w} × ${h}`)] };
}

/** Commits since a stamp's commit that change what the screenshots show. */
function changedSince(commit: string | undefined): number | null {
  if (!commit || git("cat-file", "-e", `${commit}^{commit}`) === null) return null;
  const n = git("rev-list", "--count", `${commit}..HEAD`, "--", "apple/iOS", "apple/Sources/HammockKit", "apple/Resources/agent.html", "marketing/appstore/scenes.json");
  return n === null ? null : Number(n);
}

// --- Screenshots.
const SHOT_FIX = "just appstore-screenshots";
// A folder's stamp covers every device captured into it (the iPhone and iPad share one), so it's judged once.
const setFlagsByDir = new Map<string, { level: Level; text: string }[]>();
function setFlagsFor(dir: string, d: (typeof DEVICES)[number], a: "light" | "dark", stamp: Record<string, string> | null, any: boolean) {
  if (setFlagsByDir.has(dir)) return setFlagsByDir.get(dir)!;
  const sharing = DEVICES.filter((x) => x.shots === d.shots);
  const name = `${sharing.map((x) => x.name).join(" and ")} ${a}`;
  const fix = `${SHOT_FIX} ${sharing.map((x) => `--device ${x.id}`).join(" ")} --${a}`;
  const fs: { level: Level; text: string }[] = [];
  if (any && !stamp) fs.push(flag("warn", "screenshots", `${shown(dir)} has no .captured stamp`, fix));
  if (stamp?.version && stamp.version !== iosVersion) {
    fs.push(flag("error", "screenshots", `${name}: captured from ${stamp.version}, not ${iosVersion} (the push refuses it)`, fix));
  } else {
    const since = changedSince(stamp?.commit);
    if (since) fs.push(flag("warn", "screenshots", `${name}: the app has changed in ${plural(since, "commit")} since they were captured (${stamp!.commit})`, fix));
  }
  setFlagsByDir.set(dir, fs);
  return fs;
}
const shotSections = DEVICES.map((d) => {
  const tiles: Tile[] = [];
  const stamps: string[] = [];
  for (const a of APPEARANCES) {
    const dir = shotDir(d, a);
    const stamp = stampOf(join(dir, ".captured"));
    const any = scenes.some((s) => existsSync(join(dir, `${s.id}-${d.tag}.png`)));
    const setFlags = setFlagsFor(dir, d, a, stamp, any);
    if (stamp) {
      stamps.push(`<div class="stamp"><b>${a === "dark" ? "Dark" : "Light"}</b> ${esc(stamp.version ?? "?")} · ${esc(stamp.commit ?? "?")} · ${
        stamp.captured_at ? `<time datetime="${esc(stamp.captured_at)}">${esc(ago(Date.parse(stamp.captured_at)))}</time>` : "?"}${setFlags.length ? setFlags.map((f) => badge(f.level, f.text.replace(/^[^:]*: /, ""))).join("") : badge("ok", "Current")}</div>`);
    } else if (!any) {
      flag("error", "screenshots", `missing: every ${d.name} ${a} screenshot`, `${SHOT_FIX} --device ${d.id} --${a}`);
    }
    for (const s of scenes) {
      const file = join(dir, `${s.id}-${d.tag}.png`);
      let fs: { level: Level; text: string }[] = [];
      let size = { w: d.w, h: d.h };
      if (!existsSync(file)) { if (any) fs.push(flag("error", "screenshots", `missing: ${shown(file)}`, `${SHOT_FIX} ${s.id} --device ${d.id} --${a}`)); else fs.push({ level: "error", text: "missing" }); }
      else { const c = sizeCheck("screenshots", file, d.w, d.h, d.alt); fs = c.f; if (c.s) size = c.s; }
      tiles.push({ file, title: s.id, sub: s.what, w: size.w, h: size.h, flags: fs, group: a, fix: `${SHOT_FIX} ${s.id} --device ${d.id} --${a}` });
    }
  }
  return { d, tiles, stamps };
});

// --- Marketing images (each listing locale gets the same; en-GB's are shown, the others compared to it).
const MK_FIX = "just appstore-marketing";
const mkJSON = join(APPSTORE, "marketing.json");
const mkSections = DEVICES.map((d) => {
  const tiles: Tile[] = [];
  const sourceStamp = readIf(join(APPSTORE, d.shots, CAPTURE_LOCALE, ".captured"));
  const notes: string[] = [];
  for (const loc of locales) {
    const dir = join(APPSTORE, d.out, loc);
    const made = readIf(join(dir, ".captured"));
    const have = mk.panels.filter((_, i) => existsSync(join(dir, `${String(i + 1).padStart(2, "0")}-${mk.panels[i].id}-${d.tag}.png`)));
    if (have.length && made !== sourceStamp) flag("error", "marketing", `${shown(dir)}: composed from an older capture (the push refuses it)`, `${MK_FIX} --device ${d.id}`);
    if (!have.length) flag("error", "marketing", `missing: every ${d.name} marketing image for ${loc}`, `${MK_FIX} --device ${d.id}`);
    if (loc !== CAPTURE_LOCALE) {
      const diff = mk.panels.filter((p, i) => {
        const n = `${String(i + 1).padStart(2, "0")}-${p.id}-${d.tag}.png`;
        const a = join(APPSTORE, d.out, CAPTURE_LOCALE, n), b = join(dir, n);
        return existsSync(a) !== existsSync(b) || (existsSync(a) && md5(a) !== md5(b));
      });
      notes.push(`${loc}: ${diff.length ? `${plural(diff.length, "image")} differ from ${CAPTURE_LOCALE}` : `identical to ${CAPTURE_LOCALE}`}`);
      if (diff.length) flag("warn", "marketing", `${d.name}: ${loc}'s marketing images differ from ${CAPTURE_LOCALE}'s (${diff.map((p) => p.id).join(", ")})`, MK_FIX);
    }
  }
  mk.panels.forEach((p, i) => {
    const file = join(APPSTORE, d.out, CAPTURE_LOCALE, `${String(i + 1).padStart(2, "0")}-${p.id}-${d.tag}.png`);
    const src = join(shotDir(d, p.theme), `${p.shot}-${d.tag}.png`);
    let fs: { level: Level; text: string }[] = [];
    let size = { w: d.w, h: d.h };
    if (!existsSync(file)) fs.push({ level: "error", text: "missing" });
    else {
      const c = sizeCheck("marketing", file, d.w, d.h, d.alt); fs = c.f; if (c.s) size = c.s;
      if (mtime(file) < mtime(mkJSON)) fs.push(flag("warn", "marketing", `${d.name} ${p.id}: older than marketing.json`, `${MK_FIX} --only ${p.id} --device ${d.id}`));
      if (existsSync(src) && mtime(file) < mtime(src)) fs.push(flag("warn", "marketing", `${d.name} ${p.id}: older than its screenshot`, `${MK_FIX} --only ${p.id} --device ${d.id}`));
    }
    tiles.push({ file, title: `${String(i + 1).padStart(2, "0")} ${p.id}`, sub: `${p.headline.replace(/\*/g, "").replace(/\n/g, " ")} — ${p.subline}`,
      w: size.w, h: size.h, flags: fs, group: p.theme, fix: `${MK_FIX} --only ${p.id} --device ${d.id}` });
  });
  const sheet = join(APPSTORE, d.out, `preview-${d.tag}.png`);
  const newest = Math.max(0, ...tiles.filter((t) => existsSync(t.file)).map((t) => mtime(t.file)));
  const sheetFlags = existsSync(sheet) && mtime(sheet) < newest ? [flag("warn", "marketing", `${shown(sheet)} is older than its set`, `${MK_FIX} --device ${d.id}`)] : [];
  return { d, tiles, notes, sheet, sheetFlags };
});

// --- The videos.
const editSha = sha256(join(APPSTORE, "preview.json"));
const VIDEO_FIX = "just appstore-preview-video --edit-only";
const VIDEOS = [
  { id: "preview", name: "App preview", dir: "previews", file: "01-hammock-preview.mp4", w: 886, h: 1920, audio: true,
    spec: "886 × 1920, 30 fps, H.264, a silent sound track. Placed on the 6.9\" iPhone and the iPhone Duo." },
  { id: "search", name: "Search results video", dir: "creative", file: "search-results.mp4", w: 2400, h: 1600, audio: false,
    spec: "2400 × 1600 (3:2), 30 fps, no sound. On iOS 27 it replaces the preview and screenshots in search; its caption and screen sit in the art safe area (56.5% × 40.2%, centred)." },
];
const poster = join(APPSTORE, "previews/poster.png");
/** A VP9 copy of a video, for browsers without H.264 (Hammock's browser panes, built without it), made once per
 *  version of the video into marketing/build/review/ (gitignored). The page offers the MP4 first. */
function webmCopy(mp4: string): string | null {
  const out = join(dirname(APPSTORE), "build/review", relative(APPSTORE, mp4).replace(/\.mp4$/, ".webm"));
  if (existsSync(out) && mtime(out) >= mtime(mp4)) return out;
  mkdirSync(dirname(out), { recursive: true });
  console.log(`  making a VP9 copy of ${shown(mp4)} for browsers without H.264…`);
  const r = spawnSync("ffmpeg", ["-v", "error", "-y", "-i", mp4, "-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8",
    "-row-mt", "1", "-crf", "32", "-b:v", "0", "-an", out], { stdio: "inherit" });
  return r.status === 0 ? out : null;
}
const videos = VIDEOS.map((v) => {
  const file = join(APPSTORE, v.dir, CAPTURE_LOCALE, v.file);
  const fs: { level: Level; text: string }[] = [];
  let info: Probe | null = null;
  const have = locales.filter((l) => existsSync(join(APPSTORE, v.dir, l, v.file)));
  if (!have.length) fs.push(flag("error", "videos", `missing: the ${v.name.toLowerCase()}`, "just appstore-preview-video"));
  for (const l of locales.filter((l) => !have.includes(l)).filter(() => have.length)) fs.push(flag("error", "videos", `${v.name}: none for ${l}`, "just appstore-preview-video"));
  for (const l of have) {
    const made = stampOf(join(APPSTORE, v.dir, l, ".made"));
    if (made?.edit !== editSha) fs.push(flag("error", "videos", `${v.name} (${l}): cut to another preview.json (the push refuses it)`, VIDEO_FIX));
  }
  if (existsSync(file)) {
    info = probe(file);
    if (!info) fs.push(flag("warn", "videos", `${v.name}: ffprobe couldn't read it`));
    else {
      if (info.w !== v.w || info.h !== v.h) fs.push(flag("error", "videos", `${v.name} is ${info.w} × ${info.h}, not ${v.w} × ${v.h}`, VIDEO_FIX));
      if (Math.round(info.fps) !== 30) fs.push(flag("error", "videos", `${v.name} is ${info.fps.toFixed(2)} fps, not 30`, VIDEO_FIX));
      if (v.audio && !info.audio) fs.push(flag("error", "videos", `${v.name} has no sound track (App Store Connect wants one, silent)`, VIDEO_FIX));
      if (info.seconds > 30 || info.seconds < 15) fs.push(flag("error", "videos", `${v.name} runs ${info.seconds.toFixed(1)} s (15–30 s allowed)`, VIDEO_FIX));
    }
  }
  return { v, file, info, fs, made: stampOf(join(APPSTORE, v.dir, CAPTURE_LOCALE, ".made")) };
});
const previewFile = videos[0].file;
const posterFlags: { level: Level; text: string }[] = [];
if (existsSync(previewFile) && !existsSync(poster)) posterFlags.push(flag("warn", "videos", "missing: previews/poster.png", VIDEO_FIX));
if (existsSync(poster) && existsSync(previewFile) && mtime(poster) < mtime(previewFile)) posterFlags.push(flag("warn", "videos", "previews/poster.png is older than the preview", VIDEO_FIX));
const editSeconds = edit.shots.reduce((t, s) => t + s.seconds, 0) + edit.end.seconds;

// --- The product page header.
const HEADER = { w: 3840, h: 1646, safe: { w: 1646, h: 661 } };
const headerFile = join(APPSTORE, "creative", CAPTURE_LOCALE, "header.png");
const headerPreview = join(APPSTORE, "creative/header-preview.png");
const headerFlags: { level: Level; text: string }[] = [];
const HEADER_FIX = "just appstore-marketing --only header";
if (!existsSync(headerFile)) headerFlags.push(flag("error", "header", "missing: the product page header", HEADER_FIX));
else {
  headerFlags.push(...sizeCheck("header", headerFile, HEADER.w, HEADER.h).f);
  if (mtime(headerFile) < mtime(mkJSON)) headerFlags.push(flag("warn", "header", "the header is older than marketing.json", HEADER_FIX));
  if (existsSync(ICON) && mtime(headerFile) < mtime(ICON)) headerFlags.push(flag("warn", "header", "the header is older than the app icon", HEADER_FIX));
  for (const l of locales.slice(1)) {
    const other = join(APPSTORE, "creative", l, "header.png");
    if (!existsSync(other)) headerFlags.push(flag("error", "header", `the header: none for ${l}`, HEADER_FIX));
    else if (md5(other) !== md5(headerFile)) headerFlags.push(flag("warn", "header", `the header: ${l}'s differs from ${CAPTURE_LOCALE}'s`, HEADER_FIX));
  }
}

// ============================================================================================================
// The page.
flags.sort((a, b) => (a.level === b.level ? 0 : a.level === "error" ? -1 : 1));
const errors = flags.filter((f) => f.level === "error");
const warns = flags.filter((f) => f.level === "warn");
const generatedNone = !shotSections.some((s) => s.tiles.some((t) => existsSync(t.file))) && !videos.some((v) => existsSync(v.file)) && !existsSync(headerFile);

const SECTIONS = [
  ["overview", "Overview"], ["listing", "Listing"], ["whats-new", "What's New"], ["review", "App Review"],
  ["testflight", "TestFlight"], ["app-info", "Categories & rating"], ["privacy", "Privacy"],
  ["screenshots", "Screenshots"], ["marketing", "Marketing images"], ["videos", "Videos"], ["header", "Product page header"],
] as const;
const countFor = (id: string) => {
  const e = flags.filter((f) => f.section === id && f.level === "error").length;
  const w = flags.filter((f) => f.section === id && f.level === "warn").length;
  return e ? `<span class="dot error" title="${plural(e, "problem")}">${e}</span>` : w ? `<span class="dot warn" title="${plural(w, "warning")}">${w}</span>` : "";
};

function tabs(name: string, items: { id: string; label: string; note?: string; html: string }[]) {
  return `<div class="tabs" data-tabs="${name}"><div class="tablist" role="tablist">${items.map((t, i) =>
    `<button role="tab" aria-selected="${i === 0}" data-tab="${t.id}">${esc(t.label)}${t.note ? `<small>${esc(t.note)}</small>` : ""}</button>`).join("")}</div>${
    items.map((t, i) => `<div role="tabpanel" data-panel="${t.id}"${i ? " hidden" : ""}>${t.html}</div>`).join("")}</div>`;
}

function gallery(id: string, tiles: Tile[], wide: boolean, groups: { id: string; label: string }[]) {
  return groups.map((g) => {
    const ts = g.id === "all" ? tiles : tiles.filter((t) => t.group === g.id);
    if (!ts.length) return "";
    return `<div class="gallery-group" data-appearance="${g.id}">${g.label ? `<h4 class="group-label">${esc(g.label)}</h4>` : ""}<div class="gallery${wide ? " wide" : ""}">${ts.map((t) => {
      const there = existsSync(t.file);
      const ratio = `${t.w} / ${t.h}`;
      const caption = `<figcaption><div class="cap-top"><b>${esc(t.title)}</b>${g.id === "all" ? `<span class="theme-chip ${t.group}">${t.group === "dark" ? "Dark" : "Light"}</span>` : ""}${flagBadges(t.flags.filter((f) => f.text !== "missing"))}</div><span>${esc(t.sub)}</span></figcaption>`;
      if (!there) {
        return `<figure class="tile missing" data-appearance="${t.group}"><div class="frame" style="aspect-ratio:${ratio}"><div class="missing-box">${badge("error", "Missing")}<code>${esc(t.fix)}</code></div></div>${caption}</figure>`;
      }
      return `<figure class="tile" data-appearance="${t.group}"><a class="frame shot" href="${url(t.file)}" data-gallery="${id}" data-title="${esc(t.title)}" data-sub="${esc(t.sub)}" data-meta="${esc(`${shown(t.file)} · ${t.w} × ${t.h} · ${fmtBytes(statSync(t.file).size)} · ${ago(mtime(t.file))}`)}" style="aspect-ratio:${ratio}"><img src="${url(t.file)}" alt="${esc(`${t.title}: ${t.sub}`)}" loading="lazy" decoding="async"></a>${caption}</figure>`;
    }).join("")}</div></div>`;
  }).join("");
}

const appearanceSwitch = (scope: string) => `<div class="seg" data-seg="${scope}" role="group" aria-label="Appearance">
  <button aria-pressed="true" data-show="all">Both</button><button aria-pressed="false" data-show="light">Light</button><button aria-pressed="false" data-show="dark">Dark</button></div>`;

const sectionHead = (id: string, title: string, lede: string, extra = "") =>
  `<header class="sec-head"><div><h2>${esc(title)} ${countFor(id)}</h2><p class="lede">${lede}</p></div>${extra}</header>`;

const overview = `
<section id="overview" class="hero">
  <div class="hero-top">
    <span class="icon hero-icon"><img src="${url(ICON)}" alt=""></span>
    <div>
      <p class="eyebrow">App Store listing · Hammock for iPhone ${esc(iosVersion)}</p>
      <h1>Everything the store <em>will show</em>.</h1>
      <p class="lede">Built from the files on disk in <code>${esc(branch)}</code> at <code>${esc(head)}</code>, ${new Date().toLocaleString("en-GB", { dateStyle: "medium", timeStyle: "short" })}. Run <code>just appstore-review</code> again after changing anything.</p>
    </div>
  </div>
  <div class="stats">
    <div class="stat ${errors.length ? "error" : "ok"}"><b>${errors.length}</b><span>${errors.length === 1 ? "problem" : "problems"}</span></div>
    <div class="stat ${warns.length ? "warn" : "ok"}"><b>${warns.length}</b><span>stale or worth a look</span></div>
    <div class="stat"><b>${locales.length}</b><span>${esc(locales.join(", "))}</span></div>
    <div class="stat ${checkOK ? "ok" : check.status === 0 ? "" : "error"}"><b>${check.status === 0 ? "✓" : "✗"}</b><span>appstore-check</span></div>
  </div>
  ${generatedNone ? `<div class="callout"><b>No generated assets here yet.</b> The screenshots, marketing images, videos and header are gitignored, so ${inWorktree ? "a new worktree starts without them" : "they exist only once made"}: run <code>just appstore-screenshots</code>, <code>just appstore-marketing</code> and <code>just appstore-preview-video</code>, then this again.</div>` : ""}
  ${flags.length ? `<details class="issues" ${errors.length ? "open" : ""}><summary>${plural(flags.length, "flag")}, by section</summary><ul>${
    flags.map((f) => `<li class="${f.level}"><a href="#${f.section}">${esc(SECTIONS.find((s) => s[0] === f.section)?.[1] ?? f.section)}</a><span>${esc(f.text)}</span>${f.fix ? `<code>${esc(f.fix)}</code>` : ""}</li>`).join("")}</ul></details>` : `<div class="callout ok"><b>Nothing flagged.</b> Every field is within its limit and every asset is there and current.</div>`}
  <details class="check"><summary><code>just appstore-check</code> ${check.status === 0 ? badge("ok", "Passes") : badge("error", "Fails")}</summary><pre>${esc(checkLines.join("\n"))}</pre></details>
</section>`;

const listingSection = `<section id="listing">${sectionHead("listing", "Listing", "What each locale's product page says, counted as App Store Connect counts: in characters.")}${
  tabs("listing", listingTabs.map((t) => ({ id: t.id, label: t.id, note: t.note, html: t.html })))}</section>`;

const whatsNewSection = `<section id="whats-new">${sectionHead("whats-new", "What's New", `From the iPhone's release notes (NEW and FIXED) for ${esc(iosVersion)}, including those not cut yet, as <code>just appstore-push</code> sends it when ${esc(iosVersion)} is the version App Store Connect is editing. A first version sends none.`)}
  <div class="field long"><div class="field-head"><h4>${esc(iosVersion)}</h4>${whatsNew !== null ? meter(chars(whatsNew), 4000) : ""}</div>${
    whatsNew === null ? `<p class="muted">Couldn't be made.</p>` : whatsNew ? `<div class="prose pre">${esc(whatsNew)}</div>` : `<p class="muted">No NEW or FIXED notes yet.</p>`}</div></section>`;

const reviewSection = `<section id="review">${sectionHead("review", "App Review", "What App Review is told. The contact (name, email, phone) comes from <code>marketing/.env.appstore</code> and isn't shown here.")}
  <div class="field long"><div class="field-head"><h4>Notes</h4>${reviewNotes !== null ? meter(chars(reviewNotes), 4000) : flagBadges([{ level: "error", text: "missing" }])}</div>${
    reviewNotes ? `<div class="prose pre">${esc(reviewNotes)}</div>` : ""}</div>
  <div class="field"><div class="field-head"><h4>Demo video</h4></div><a class="url" href="https://homegrownsw.com/hammock/demo.mp4" target="_blank" rel="noopener">https://homegrownsw.com/hammock/demo.mp4</a><p class="muted small">Kept in the site repo, not made here.</p></div></section>`;

const testflightSection = `<section id="testflight">${sectionHead("testflight", "TestFlight", "The Beta App Description and URLs testers see. Its feedback email is App Review's contact.")}${
  tfLocales.length ? tabs("testflight", tfLocales.map((loc) => {
    const d = join(APPSTORE, "testflight", loc);
    const desc = readIf(join(d, "description.txt")) ?? "";
    const same = loc !== tfLocales[0] && desc === readIf(join(APPSTORE, "testflight", tfLocales[0], "description.txt"));
    return { id: `tf-${loc}`, label: loc, note: same ? `identical to ${tfLocales[0]}` : undefined, html: `<div class="fields">
      <div class="field long"><div class="field-head"><h4>Beta App Description</h4>${meter(chars(desc), 4000)}</div><div class="prose pre">${esc(desc)}</div></div>
      ${["marketing_url.txt", "privacy_url.txt"].map((f) => { const u = readIf(join(d, f)); return `<div class="field"><div class="field-head"><h4>${f === "marketing_url.txt" ? "Marketing URL" : "Privacy policy URL"}</h4></div>${u ? `<a class="url" href="${esc(u)}" target="_blank" rel="noopener">${esc(u)}</a>` : `<span class="muted">not set</span>`}</div>`; }).join("")}</div>` };
  })) : `<p class="muted">No TestFlight locales: a push leaves TestFlight's test information alone.</p>`}</section>`;

const age = json(join(APPSTORE, "age_rating.json")) as Record<string, string | boolean>;
const humanKey = (k: string) => k.replace(/([A-Z])/g, " $1").replace(/^./, (c) => c.toUpperCase()).replace(/ Or /g, " or ").replace(/ And /g, " and ");
const ageRows = Object.entries(age).map(([k, v]) => {
  const raised = v === true || (typeof v === "string" && v !== "NONE");
  const val = typeof v === "boolean" ? (v ? "Yes" : "No") : v.replace(/_/g, " ").toLowerCase().replace(/^./, (c) => c.toUpperCase());
  return `<tr class="${raised ? "raised" : ""}"><td>${esc(humanKey(k))}</td><td>${raised ? badge("warn", val) : `<span class="muted">${esc(val)}</span>`}</td></tr>`;
}).join("");
const appInfoSection = `<section id="app-info">${sectionHead("app-info", "Categories & rating", "The app-wide fields, and the age rating questionnaire as it's answered.")}
  <div class="kv">
    <div><span>Primary category</span><b>${esc(CATEGORY_NAMES[appInfo.primary ?? ""] ?? appInfo.primary ?? "—")}</b></div>
    <div><span>Secondary category</span><b>${esc(CATEGORY_NAMES[appInfo.secondary ?? ""] ?? appInfo.secondary ?? "—")}</b></div>
    <div><span>Copyright</span><b>© ${esc(appInfo.copyright ?? "—")}</b></div>
  </div>
  <div class="card table-card"><h3>Age rating</h3><p class="muted small">Highlighted answers raise the rating. Unrestricted web access is answered yes because the app shows the Mac's browser panes.</p><table class="age">${ageRows}</table></div></section>`;

const privacySection = `<section id="privacy">${sectionHead("privacy", "Privacy", "The App Privacy label, entered by hand in App Store Connect from <code>app-privacy.md</code>.")}
  <div class="card prose md">${markdown(read(join(APPSTORE, "app-privacy.md")).replace(/^# .*\n/, ""))}</div></section>`;

const shotsSection = `<section id="screenshots">${sectionHead("screenshots", "Screenshots", `The demo in the Simulator, as <code>just appstore-screenshots</code> takes it: ${scenes.length} scenes, light and dark, in ${CAPTURE_LOCALE} (one set serves every locale). The listing shows the marketing images composed from these.`, appearanceSwitch("screenshots"))}${
  tabs("shots", shotSections.map(({ d, tiles, stamps }) => ({
    id: `shots-${d.id}`, label: d.name, note: `${d.w} × ${d.h}`,
    html: `${stamps.length ? `<div class="stamps">${stamps.join("")}</div>` : ""}${gallery(`shots-${d.id}`, tiles, d.id === "duo", [{ id: "light", label: "Light" }, { id: "dark", label: "Dark" }])}`,
  })))}</section>`;

const mkSection = `<section id="marketing">${sectionHead("marketing", "Marketing images", `What the listing shows, in order: each screenshot in its device's frame under the copy in <code>marketing.json</code>. ${CAPTURE_LOCALE}'s are shown; the other locales are compared to them.`, appearanceSwitch("marketing"))}${
  tabs("marketing", mkSections.map(({ d, tiles, notes, sheet, sheetFlags }) => ({
    id: `mk-${d.id}`, label: d.name, note: `${tiles.filter((t) => existsSync(t.file)).length}/${tiles.length}`,
    html: `<div class="stamps">${notes.map((n) => `<div class="stamp">${esc(n)}</div>`).join("")}${existsSync(sheet) ? `<a class="stamp link shot" href="${url(sheet)}" data-gallery="sheet-${d.id}" data-title="Contact sheet" data-sub="${esc(d.name)}" data-meta="${esc(shown(sheet))}">Contact sheet ↗${flagBadges(sheetFlags)}</a>` : ""}</div>${
      gallery(`mk-${d.id}`, tiles, d.id === "duo", [{ id: "all", label: "" }])}`,
  })))}</section>`;

const videoCards = videos.map(({ v, file, info, fs, made }) => {
  const there = existsSync(file);
  const ratio = `${v.w} / ${v.h}`;
  const player = there
    ? `<video controls preload="metadata" playsinline ${v.id === "preview" && existsSync(poster) ? `poster="${url(poster)}"` : ""} style="aspect-ratio:${ratio}"><source src="${url(file)}" type="video/mp4">${
      (() => { const w = webmCopy(file); return w ? `<source src="${url(w)}" type="video/webm">` : ""; })()}</video>`
    : `<div class="missing-box video" style="aspect-ratio:${ratio}">${badge("error", "Missing")}<code>just appstore-preview-video</code></div>`;
  const facts = info ? [`${info.w} × ${info.h}`, `${info.fps.toFixed(info.fps % 1 ? 2 : 0)} fps`, `${info.seconds.toFixed(1)} s`, info.codec.toUpperCase(), info.audio ? `sound: ${info.audio}` : "no sound", fmtBytes(info.bytes)] : [];
  return `<div class="card video-card ${v.id}"><div class="player">${player}${v.id === "search" && there ? `<div class="safe" style="width:56.5%;height:40.2%" title="Art safe area"></div>` : ""}</div>
    <div class="video-meta"><h3>${esc(v.name)} ${flagBadges(fs)}${there && !fs.length ? badge("ok", "Current") : ""}</h3><p class="muted small">${esc(v.spec)}</p>
    ${facts.length ? `<div class="facts">${facts.map((f) => `<span>${esc(f)}</span>`).join("")}</div>` : ""}
    ${made ? `<p class="muted small">Cut at ${esc(made.commit ?? "?")} from preview.json ${esc((made.edit ?? "").slice(0, 10))}${there ? `, ${ago(mtime(file))}` : ""}.</p>` : ""}
    ${there ? `<p class="muted small"><code>${esc(shown(file))}</code></p>` : ""}</div></div>`;
}).join("");
const storyboard = `<div class="card"><h3>The edit <span class="muted small">${editSeconds.toFixed(1)} s before crossfades · poster at ${edit.poster} s</span></h3>
  <ol class="board">${edit.shots.map((s) => `<li><span class="t">${s.seconds.toFixed(1)} s</span><div class="cap">${copyHTML(s.caption)}</div><span class="muted small">${esc(s.take)} @ ${esc(s.mark)} + ${s.from}s</span></li>`).join("")}
  <li class="end"><span class="t">${edit.end.seconds.toFixed(1)} s</span><div class="cap">${copyHTML(edit.end.headline)}</div><span class="muted small">${esc(edit.end.subline)}</span></li></ol></div>`;
const posterCard = existsSync(poster)
  ? `<figure class="card poster"><a class="shot" href="${url(poster)}" data-gallery="poster" data-title="Poster frame" data-sub="The app preview at ${edit.poster} s" data-meta="${esc(shown(poster))}"><img src="${url(poster)}" alt="The app preview's poster frame" loading="lazy"></a><figcaption><b>Poster frame</b> at ${edit.poster} s ${flagBadges(posterFlags)}</figcaption></figure>`
  : `<figure class="card poster"><div class="missing-box" style="aspect-ratio:886/1920">${badge("error", "Missing")}<code>${VIDEO_FIX}</code></div><figcaption><b>Poster frame</b> at ${edit.poster} s</figcaption></figure>`;
const videoSection = `<section id="videos">${sectionHead("videos", "Videos", "Cut from takes recorded in the Simulator, the same shots for both, by <code>just appstore-preview-video</code>.",
  existsSync(videos[1].file) ? `<label class="toggle"><input type="checkbox" class="safe-toggle" checked> Safe area</label>` : "")}
  <div class="videos">${videoCards}${posterCard}</div>${storyboard}</section>`;

const headerSection = `<section id="header">${sectionHead("header", "Product page header", `Across the top of the product page on iOS 27: ${HEADER.w} × ${HEADER.h}. Devices crop it differently; only the art safe area (${HEADER.safe.w} × ${HEADER.safe.h}, centred) is sure to show.`,
  existsSync(headerFile) ? `<label class="toggle"><input type="checkbox" class="safe-toggle" checked> Safe area</label>` : "")}
  <div class="card header-card">${existsSync(headerFile)
    ? `<a class="shot header-img" href="${url(headerFile)}" data-gallery="header" data-title="Product page header" data-sub="${esc(`${mk.header.wordmark} — ${mk.header.tagline}`)}" data-meta="${esc(shown(headerFile))}"><img src="${url(headerFile)}" alt="The product page header"><span class="safe" style="width:${(HEADER.safe.w / HEADER.w * 100).toFixed(3)}%;height:${(HEADER.safe.h / HEADER.h * 100).toFixed(3)}%"></span></a>`
    : `<div class="missing-box" style="aspect-ratio:${HEADER.w}/${HEADER.h}">${badge("error", "Missing")}<code>${HEADER_FIX}</code></div>`}
    <div class="header-meta"><div><b>${esc(mk.header.wordmark)}</b> <span class="muted">${esc(mk.header.tagline)}</span></div>${flagBadges(headerFlags)}${existsSync(headerFile) && !headerFlags.length ? badge("ok", "Current") : ""}${
      existsSync(headerPreview) ? `<a class="small" href="${url(headerPreview)}" target="_blank">header-preview.png ↗</a>` : ""}</div></div></section>`;

const CSS = String.raw`
:root {
  --bg: #f3f8f4; --bg-2: #e7f0ea; --card: rgba(255,255,255,.72); --card-solid: #fff; --line: rgba(6,20,14,.10); --line-2: rgba(6,20,14,.18);
  --text: #06140e; --muted: #3f5c4d; --faint: #6f8f7f; --mint: #1f9d5a; --mint-ink: #0d7a43;
  --accent: linear-gradient(90deg, #1f9d5a, #0f8f8a 45%, #0a6fd6 75%, #7a3fd0);
  --glow: radial-gradient(1200px 520px at 50% -180px, rgba(61,255,162,.30), transparent 70%), radial-gradient(800px 500px at 0% 100%, rgba(191,90,242,.06), transparent 70%);
  --ok: #1a8f4c; --ok-bg: rgba(26,143,76,.12); --warn: #a15c00; --warn-bg: rgba(255,159,10,.16); --error: #c4281c; --error-bg: rgba(255,69,58,.13);
  --shadow: 0 1px 2px rgba(6,20,14,.06), 0 12px 32px -12px rgba(6,20,14,.18); --tile: #e9f1ec; --code-bg: rgba(6,20,14,.06);
  --display: "Space Grotesk", Inter, system-ui, sans-serif; --mono: "JetBrains Mono", ui-monospace, monospace; --radius: 18px;
  color-scheme: light;
}
@media (prefers-color-scheme: dark) { :root:not([data-theme="light"]) {
  --bg: #06140e; --bg-2: #0b241a; --card: rgba(255,255,255,.045); --card-solid: #0c1f17; --line: rgba(255,255,255,.09); --line-2: rgba(255,255,255,.16);
  --text: #eaf6ef; --muted: #a3c2b2; --faint: #6f8f7f; --mint: #3dffa2; --mint-ink: #3dffa2;
  --accent: linear-gradient(90deg, #61d836, #ffd60a 22%, #ff9f0a 40%, #ff453a 58%, #bf5af2 78%, #0a84ff);
  --glow: radial-gradient(1300px 600px at 50% -200px, rgba(46,138,98,.42), transparent 70%), radial-gradient(800px 500px at 90% 100%, rgba(10,132,255,.07), transparent 70%);
  --ok: #3dffa2; --ok-bg: rgba(61,255,162,.12); --warn: #ffbf47; --warn-bg: rgba(255,159,10,.16); --error: #ff7a70; --error-bg: rgba(255,69,58,.16);
  --shadow: 0 1px 2px rgba(0,0,0,.3), 0 18px 40px -16px rgba(0,0,0,.6); --tile: #081a12; --code-bg: rgba(255,255,255,.07);
  color-scheme: dark;
} }
:root[data-theme="dark"] {
  --bg: #06140e; --bg-2: #0b241a; --card: rgba(255,255,255,.045); --card-solid: #0c1f17; --line: rgba(255,255,255,.09); --line-2: rgba(255,255,255,.16);
  --text: #eaf6ef; --muted: #a3c2b2; --faint: #6f8f7f; --mint: #3dffa2; --mint-ink: #3dffa2;
  --accent: linear-gradient(90deg, #61d836, #ffd60a 22%, #ff9f0a 40%, #ff453a 58%, #bf5af2 78%, #0a84ff);
  --glow: radial-gradient(1300px 600px at 50% -200px, rgba(46,138,98,.42), transparent 70%), radial-gradient(800px 500px at 90% 100%, rgba(10,132,255,.07), transparent 70%);
  --ok: #3dffa2; --ok-bg: rgba(61,255,162,.12); --warn: #ffbf47; --warn-bg: rgba(255,159,10,.16); --error: #ff7a70; --error-bg: rgba(255,69,58,.16);
  --shadow: 0 1px 2px rgba(0,0,0,.3), 0 18px 40px -16px rgba(0,0,0,.6); --tile: #081a12; --code-bg: rgba(255,255,255,.07);
  color-scheme: dark;
}
* { box-sizing: border-box; }
html { background: var(--bg); scroll-behavior: smooth; scroll-padding-top: 76px; -webkit-text-size-adjust: 100%; }
body { margin: 0; background: var(--glow), var(--bg); background-repeat: no-repeat; color: var(--text); font: 15px/1.6 Inter, system-ui, -apple-system, sans-serif; overflow-x: clip; -webkit-font-smoothing: antialiased; }
a { color: var(--mint-ink); text-underline-offset: 3px; }
code { font: 500 .86em var(--mono); background: var(--code-bg); padding: .1em .4em; border-radius: 6px; }
em { font-style: normal; background: var(--accent); -webkit-background-clip: text; background-clip: text; color: transparent; }
.muted { color: var(--muted); } .small { font-size: 13px; }

.top { position: sticky; top: 0; z-index: 40; display: flex; align-items: center; gap: 16px; padding: 12px 24px;
  background: color-mix(in srgb, var(--bg) 78%, transparent); -webkit-backdrop-filter: saturate(1.4) blur(16px); backdrop-filter: saturate(1.4) blur(16px); border-bottom: 1px solid var(--line); }
.brand { display: flex; align-items: center; gap: 10px; font: 700 19px var(--display); letter-spacing: -.02em; color: var(--text); text-decoration: none; white-space: nowrap; }
.brand small { font: 500 13px Inter, sans-serif; color: var(--faint); letter-spacing: 0; }
.icon { display: inline-block; width: 30px; height: 30px; border-radius: 22.5%; overflow: hidden; background: linear-gradient(180deg, rgb(29,96,70), rgb(7,26,18)); box-shadow: inset 0 0 0 1px rgba(255,255,255,.08); flex: none; }
.icon img { width: 100%; height: 100%; display: block; }
.icon.big { width: 72px; height: 72px; } .hero-icon { width: 88px; height: 88px; box-shadow: var(--shadow), inset 0 0 0 1px rgba(255,255,255,.08); }
.top .spacer { flex: 1; }
.theme { display: flex; padding: 3px; border-radius: 10px; background: var(--code-bg); }
.theme button, .seg button { border: 0; background: none; color: var(--muted); font: 500 13px Inter, sans-serif; padding: 5px 10px; border-radius: 7px; cursor: pointer; }
.theme button[aria-pressed="true"], .seg button[aria-pressed="true"] { background: var(--card-solid); color: var(--text); box-shadow: 0 1px 3px rgba(0,0,0,.15); }

.layout { display: grid; grid-template-columns: 220px minmax(0, 1fr); gap: 40px; max-width: 1440px; margin: 0 auto; padding: 0 24px 80px; }
.side { position: sticky; top: 72px; align-self: start; padding-top: 28px; }
.side a { display: flex; align-items: center; justify-content: space-between; gap: 8px; padding: 7px 12px; border-radius: 10px; color: var(--muted); text-decoration: none; font-weight: 500; font-size: 14px; }
.side a:hover { color: var(--text); background: var(--code-bg); }
.side a.active { color: var(--text); background: var(--card); box-shadow: inset 0 0 0 1px var(--line); }
.dot { display: inline-grid; place-items: center; min-width: 20px; height: 20px; padding: 0 6px; border-radius: 10px; font: 600 11px Inter, sans-serif; }
.dot.error { background: var(--error-bg); color: var(--error); } .dot.warn { background: var(--warn-bg); color: var(--warn); }
h2 .dot { vertical-align: middle; margin-left: 6px; }

main { min-width: 0; }
section { padding-top: 44px; }
.hero { padding-top: 40px; }
.hero-top { display: flex; gap: 24px; align-items: center; }
.eyebrow { margin: 0 0 6px; font: 600 12px Inter, sans-serif; letter-spacing: .08em; text-transform: uppercase; color: var(--faint); }
h1 { margin: 0 0 8px; font: 700 clamp(32px, 4.4vw, 52px)/1.04 var(--display); letter-spacing: -.035em; }
h2 { margin: 0; font: 700 28px/1.15 var(--display); letter-spacing: -.025em; }
h3 { margin: 0 0 8px; font: 600 17px/1.3 var(--display); letter-spacing: -.01em; display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
h4 { margin: 0; font: 600 13px Inter, sans-serif; letter-spacing: .04em; text-transform: uppercase; color: var(--faint); }
.lede { margin: 4px 0 0; color: var(--muted); max-width: 72ch; }
.sec-head { display: flex; align-items: flex-end; justify-content: space-between; gap: 20px; margin-bottom: 18px; flex-wrap: wrap; }

.stats { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 12px; margin: 28px 0 16px; }
.stat { padding: 16px 18px; border-radius: var(--radius); background: var(--card); border: 1px solid var(--line); }
.stat b { display: block; font: 700 32px/1 var(--display); letter-spacing: -.03em; }
.stat span { color: var(--muted); font-size: 13px; }
.stat.error b { color: var(--error); } .stat.warn b { color: var(--warn); } .stat.ok b { color: var(--ok); }
.callout { padding: 14px 18px; border-radius: 14px; background: var(--warn-bg); margin: 12px 0; }
.callout.ok { background: var(--ok-bg); }
details { border-radius: var(--radius); background: var(--card); border: 1px solid var(--line); margin: 12px 0; }
summary { cursor: pointer; padding: 14px 18px; font-weight: 600; display: flex; align-items: center; gap: 10px; }
details pre { margin: 0; padding: 0 18px 16px; font: 13px/1.6 var(--mono); white-space: pre-wrap; color: var(--muted); }
.issues ul { list-style: none; margin: 0; padding: 0 10px 10px; }
.issues li { display: grid; grid-template-columns: 150px minmax(0, 1fr) auto; gap: 12px; align-items: baseline; padding: 8px 10px; border-radius: 10px; border-left: 3px solid; }
.issues li + li { margin-top: 2px; }
.issues li.error { border-color: var(--error); } .issues li.warn { border-color: var(--warn); }
.issues li:hover { background: var(--code-bg); }
.issues li a { font-weight: 600; font-size: 13px; text-decoration: none; }
.issues li code { white-space: nowrap; font-size: 12px; }

.badge { display: inline-flex; align-items: center; min-height: 22px; padding: 0 8px; border-radius: 6px; font: 600 11px Inter, sans-serif; letter-spacing: .02em; white-space: nowrap; }
.badge.ok { background: var(--ok-bg); color: var(--ok); } .badge.warn { background: var(--warn-bg); color: var(--warn); } .badge.error { background: var(--error-bg); color: var(--error); }

.tablist { display: flex; gap: 6px; flex-wrap: wrap; margin-bottom: 18px; }
.tablist button { display: flex; flex-direction: column; align-items: flex-start; gap: 1px; border: 1px solid var(--line); background: var(--card); color: var(--muted); font: 600 14px Inter, sans-serif; padding: 9px 16px; border-radius: 12px; cursor: pointer; text-align: left; }
.tablist button small { font: 500 12px Inter, sans-serif; color: var(--faint); }
.tablist button[aria-selected="true"] { color: var(--text); border-color: var(--mint); box-shadow: 0 0 0 1px var(--mint) inset; }

.listing { display: grid; grid-template-columns: 300px minmax(0, 1fr); gap: 20px; align-items: start; }
.store { position: sticky; top: 92px; padding: 20px; border-radius: 22px; background: var(--card-solid); border: 1px solid var(--line); box-shadow: var(--shadow); }
.store-row { display: flex; gap: 14px; align-items: flex-start; }
.store-row > div { min-width: 0; }
.store-name { font: 600 18px/1.25 -apple-system, "SF Pro Display", Inter, sans-serif; overflow: hidden; text-overflow: ellipsis; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; }
.store-sub { color: var(--muted); font-size: 14px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.get { display: inline-block; margin-top: 8px; padding: 3px 18px; border-radius: 999px; background: #0a84ff; color: #fff; font: 700 14px -apple-system, Inter, sans-serif; }
.store-promo { margin: 16px 0 0; color: var(--muted); font-size: 14px; border-top: 1px solid var(--line); padding-top: 14px; }
.fields { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }
.field { padding: 16px 18px; border-radius: var(--radius); background: var(--card); border: 1px solid var(--line); min-width: 0; }
.field.long { grid-column: 1 / -1; }
.field.empty { opacity: .7; }
.field-head { display: flex; align-items: center; gap: 8px; margin-bottom: 8px; flex-wrap: wrap; }
.field-head h4 { margin-right: auto; }
.value { font: 500 17px/1.4 var(--display); letter-spacing: -.01em; }
.url { word-break: break-all; font-weight: 500; }
.prose { color: var(--text); max-width: 80ch; }
.pre { white-space: pre-wrap; }
.same { font: 600 11px Inter, sans-serif; color: var(--faint); padding: 2px 7px; border-radius: 6px; border: 1px solid var(--line); }
.count { display: inline-flex; align-items: center; gap: 8px; font: 500 12px var(--mono); color: var(--faint); }
.count b { color: var(--text); font-weight: 600; }
.count .bar { width: 64px; height: 5px; border-radius: 3px; background: var(--code-bg); overflow: hidden; }
.count .bar i { display: block; height: 100%; background: var(--ok); border-radius: 3px; }
.count.near .bar i { background: var(--warn); } .count.over .bar i { background: var(--error); } .count.over b { color: var(--error); }
.chips { display: flex; flex-wrap: wrap; gap: 6px; }
.chip { display: inline-flex; align-items: baseline; gap: 6px; padding: 4px 10px; border-radius: 999px; background: var(--code-bg); font-weight: 500; font-size: 14px; }
.chip small { color: var(--faint); font: 11px var(--mono); }

.card { padding: 18px 20px; border-radius: var(--radius); background: var(--card); border: 1px solid var(--line); margin-bottom: 12px; min-width: 0; }
.kv { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; margin-bottom: 12px; }
.kv > div { padding: 16px 18px; border-radius: var(--radius); background: var(--card); border: 1px solid var(--line); }
.kv span { display: block; font: 600 12px Inter, sans-serif; letter-spacing: .04em; text-transform: uppercase; color: var(--faint); margin-bottom: 4px; }
.kv b { font: 600 17px var(--display); }
table { width: 100%; border-collapse: collapse; font-size: 14px; }
td, th { text-align: left; padding: 8px 10px; border-bottom: 1px solid var(--line); vertical-align: top; }
th { font: 600 12px Inter, sans-serif; letter-spacing: .04em; text-transform: uppercase; color: var(--faint); }
.age { columns: 2; } .age tr.raised td:first-child { font-weight: 600; }
.md h3, .md h4 { margin-top: 18px; } .md p, .md li { color: var(--muted); } .md strong { color: var(--text); } .md table { margin: 8px 0 16px; }
.md p:first-child { margin-top: 0; }

.seg { display: flex; padding: 3px; border-radius: 10px; background: var(--code-bg); }
.stamps { display: flex; flex-wrap: wrap; gap: 8px; margin-bottom: 16px; }
.stamp { display: inline-flex; align-items: center; gap: 8px; padding: 6px 12px; border-radius: 10px; background: var(--card); border: 1px solid var(--line); font-size: 13px; color: var(--muted); }
.stamp b { color: var(--text); }
.stamp.link { text-decoration: none; color: var(--mint-ink); font-weight: 600; }
.group-label { margin: 6px 0 10px; }
.gallery-group + .gallery-group { margin-top: 22px; }
[data-only="light"] [data-appearance="dark"], [data-only="dark"] [data-appearance="light"] { display: none; }
.theme-chip { font: 600 10px Inter, sans-serif; letter-spacing: .04em; text-transform: uppercase; padding: 2px 6px; border-radius: 5px; border: 1px solid var(--line-2); color: var(--muted); }
.theme-chip.dark { background: #06140e; color: #a3c2b2; border-color: #06140e; }
.gallery { display: grid; grid-template-columns: repeat(auto-fill, minmax(140px, 1fr)); gap: 16px; }
.gallery.wide { grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); }
.tile { margin: 0; min-width: 0; }
.frame { display: block; position: relative; border-radius: 14px; overflow: hidden; background: var(--tile); border: 1px solid var(--line); box-shadow: var(--shadow); transition: transform .18s, box-shadow .18s; }
a.frame:hover { transform: translateY(-3px); box-shadow: var(--shadow), 0 0 0 2px var(--mint); }
.frame img { display: block; width: 100%; height: 100%; object-fit: contain; }
.missing-box { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 10px; padding: 12px; height: 100%; text-align: center;
  border: 1.5px dashed var(--line-2); border-radius: 14px; background: repeating-linear-gradient(-45deg, transparent 0 10px, var(--code-bg) 10px 11px); }
.tile.missing .frame { box-shadow: none; border: 0; background: none; }
.missing-box code { font-size: 11px; word-break: break-word; }
figcaption { padding: 8px 2px 0; font-size: 13px; color: var(--muted); line-height: 1.45; }
.cap-top { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.cap-top b { color: var(--text); font: 600 13px var(--mono); }

.videos { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 2.2fr) minmax(0, .8fr); gap: 12px; align-items: start; }
.video-card { margin: 0; }
.player { position: relative; border-radius: 12px; overflow: hidden; background: #000; }
.player video { display: block; width: 100%; height: auto; }
.player .missing-box { background-color: var(--tile); }
.safe { position: absolute; left: 50%; top: 50%; transform: translate(-50%, -50%); border: 2px dashed rgba(255,255,255,.75); border-radius: 4px; pointer-events: none; box-shadow: 0 0 0 9999px rgba(0,0,0,.28); }
.video-meta { padding-top: 14px; }
.facts { display: flex; flex-wrap: wrap; gap: 6px; margin: 8px 0; }
.facts span { font: 500 12px var(--mono); padding: 3px 8px; border-radius: 6px; background: var(--code-bg); }
.poster { margin: 0; } .poster img { display: block; width: 100%; border-radius: 12px; }
.poster figcaption { display: flex; gap: 6px; align-items: center; flex-wrap: wrap; }
.board { list-style: none; margin: 10px 0 0; padding: 0; display: grid; grid-template-columns: repeat(auto-fill, minmax(170px, 1fr)); gap: 10px; }
.board li { padding: 14px; border-radius: 14px; background: #06140e; color: #eaf6ef; display: flex; flex-direction: column; gap: 8px; }
.board li .muted { color: #a3c2b2; }
.board .cap { font: 700 17px/1.1 var(--display); letter-spacing: -.02em; }
.board em { background: linear-gradient(90deg, #61d836, #ffd60a 22%, #ff9f0a 40%, #ff453a 58%, #bf5af2 78%, #0a84ff); -webkit-background-clip: text; background-clip: text; }
.board .t { font: 600 11px var(--mono); color: #3dffa2; }
.board .end { background: linear-gradient(160deg, #0b241a, #06140e); outline: 1px solid rgba(61,255,162,.3); }

.header-card { padding: 12px; }
.header-img { display: block; position: relative; border-radius: 10px; overflow: hidden; }
.header-img img { display: block; width: 100%; }
.header-img .safe { border-color: rgba(61,255,162,.9); }
body:not(.show-safe) .safe { display: none; }
.header-meta { display: flex; align-items: center; gap: 10px; padding: 12px 6px 2px; flex-wrap: wrap; }
.toggle { display: inline-flex; align-items: center; gap: 8px; font-weight: 500; font-size: 14px; color: var(--muted); cursor: pointer; }
.toggle input { accent-color: #1f9d5a; }

.lb { position: fixed; inset: 0; z-index: 100; display: none; grid-template-rows: minmax(0, 1fr) auto; background: rgba(3,10,7,.92); -webkit-backdrop-filter: blur(12px); backdrop-filter: blur(12px); color: #eaf6ef; }
.lb.open { display: grid; }
.lb .stage { display: grid; place-items: center; padding: 56px 72px 12px; min-height: 0; }
.lb img { max-width: calc(100vw - 144px); max-height: calc(100dvh - 150px); object-fit: contain; border-radius: 10px; box-shadow: 0 30px 80px rgba(0,0,0,.6); cursor: zoom-in; }
.lb.zoom .stage { overflow: auto; place-items: start center; }
.lb.zoom img { max-width: none; max-height: none; cursor: zoom-out; }
.lb .bar { display: flex; align-items: center; gap: 16px; padding: 14px 24px 20px; }
.lb .bar b { font: 600 15px var(--display); } .lb .bar span { color: #a3c2b2; font-size: 14px; }
.lb .meta { margin-left: auto; font: 12px var(--mono); color: #6f8f7f; text-align: right; }
.lb button { position: absolute; border: 0; background: rgba(255,255,255,.08); color: #eaf6ef; width: 44px; height: 44px; border-radius: 22px; font-size: 22px; cursor: pointer; display: grid; place-items: center; }
.lb button:hover { background: rgba(255,255,255,.16); }
.lb .close { top: 14px; right: 16px; } .lb .prev { left: 16px; top: 50%; } .lb .next { right: 16px; top: 50%; }
.lb .n { position: absolute; top: 24px; left: 24px; font: 12px var(--mono); color: #6f8f7f; }

@media (max-width: 1100px) {
  .layout { grid-template-columns: minmax(0, 1fr); gap: 0; }
  .side { position: sticky; top: 57px; z-index: 30; display: flex; gap: 4px; overflow-x: auto; padding: 8px 0; margin: 0 -24px; padding-inline: 24px; background: color-mix(in srgb, var(--bg) 85%, transparent); -webkit-backdrop-filter: blur(16px); backdrop-filter: blur(16px); border-bottom: 1px solid var(--line); scrollbar-width: none; }
  .side a { white-space: nowrap; flex: none; }
  html { scroll-padding-top: 120px; }
  .listing { grid-template-columns: minmax(0, 1fr); } .store { position: static; max-width: 420px; }
  .videos { grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); } .videos .search { grid-column: 1 / -1; order: -1; }
}
@media (max-width: 640px) {
  .top { padding: 10px 16px; } .brand small { display: none; }
  .layout { padding: 0 16px 60px; } .side { margin: 0 -16px; padding-inline: 16px; }
  .hero-top { flex-direction: column; align-items: flex-start; gap: 16px; }
  .stats { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .fields, .kv { grid-template-columns: minmax(0, 1fr); }
  .issues li { grid-template-columns: minmax(0, 1fr); gap: 2px; } .issues li code { white-space: normal; }
  .gallery { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; } .gallery.wide { grid-template-columns: minmax(0, 1fr); }
  .videos { grid-template-columns: minmax(0, 1fr); }
  .age { columns: 1; }
  .lb .stage { padding: 56px 8px 8px; } .lb img { max-width: calc(100vw - 16px); } .lb .prev, .lb .next { top: auto; bottom: 70px; } .lb .meta { display: none; }
}
@media (prefers-reduced-motion: reduce) { html { scroll-behavior: auto; } * { transition: none !important; } }
`;

const JS = String.raw`
const $ = (s, el = document) => el.querySelector(s), $$ = (s, el = document) => [...el.querySelectorAll(s)];
const store = { get(k) { try { return localStorage.getItem(k); } catch { return null; } }, set(k, v) { try { localStorage.setItem(k, v); } catch {} } };

// Theme: Auto follows the system; Light and Dark pin it.
const setTheme = (t) => {
  if (t === "auto") document.documentElement.removeAttribute("data-theme"); else document.documentElement.dataset.theme = t;
  $$(".theme button").forEach((b) => b.setAttribute("aria-pressed", String(b.dataset.theme === t)));
  store.set("appstore-review-theme", t);
};
setTheme(store.get("appstore-review-theme") || "auto");
$$(".theme button").forEach((b) => b.addEventListener("click", () => setTheme(b.dataset.theme)));

// Tabs, remembered per group.
$$("[data-tabs]").forEach((box) => {
  const key = "appstore-review-tab-" + box.dataset.tabs;
  const show = (id) => {
    const btn = $$('[role="tab"]', box).find((b) => b.dataset.tab === id) || $('[role="tab"]', box);
    $$('[role="tab"]', box).forEach((b) => b.setAttribute("aria-selected", String(b === btn)));
    $$(":scope > [data-panel]", box).forEach((p) => (p.hidden = p.dataset.panel !== btn.dataset.tab));
    store.set(key, btn.dataset.tab);
  };
  $$('[role="tab"]', box).forEach((b) => b.addEventListener("click", () => show(b.dataset.tab)));
  if (store.get(key)) show(store.get(key));
});

// Light / dark / both, per section.
$$("[data-seg]").forEach((seg) => {
  const section = seg.closest("section");
  $$("button", seg).forEach((b) => b.addEventListener("click", () => {
    $$("button", seg).forEach((x) => x.setAttribute("aria-pressed", String(x === b)));
    if (b.dataset.show === "all") delete section.dataset.only; else section.dataset.only = b.dataset.show;
  }));
});

// The art safe areas (the header's and the search results video's), shown or hidden together.
const safes = $$(".safe-toggle");
safes.forEach((box) => box.addEventListener("change", () => { safes.forEach((b) => (b.checked = box.checked)); document.body.classList.toggle("show-safe", box.checked); }));
document.body.classList.toggle("show-safe", safes.length > 0);

// The sidebar follows the scroll.
const links = new Map($$(".side a").map((a) => [a.getAttribute("href").slice(1), a]));
const io = new IntersectionObserver((es) => es.forEach((e) => {
  if (e.isIntersecting) { links.forEach((a) => a.classList.remove("active")); const a = links.get(e.target.id); if (a) { a.classList.add("active"); a.scrollIntoView({ block: "nearest", inline: "nearest" }); } }
}), { rootMargin: "-30% 0px -65% 0px" });
$$("main > section").forEach((s) => io.observe(s));

// The lightbox: every image in the same gallery (and visible), arrows to move, Esc to close, click to zoom.
const lb = $(".lb"), img = $(".lb img");
let list = [], at = 0;
const visible = (a) => a.offsetParent !== null;
const render = () => {
  const a = list[at];
  lb.classList.remove("zoom");
  img.src = a.getAttribute("href"); img.alt = a.dataset.title || "";
  $(".lb .bar b").textContent = a.dataset.title || ""; $(".lb .bar span").textContent = a.dataset.sub || "";
  $(".lb .meta").textContent = a.dataset.meta || ""; $(".lb .n").textContent = list.length > 1 ? (at + 1) + " / " + list.length : "";
  $(".lb .prev").hidden = $(".lb .next").hidden = list.length < 2;
};
const openLb = (a) => { list = $$('a.shot[data-gallery="' + a.dataset.gallery + '"]').filter(visible); at = Math.max(0, list.indexOf(a)); render(); lb.classList.add("open"); document.body.style.overflow = "hidden"; $(".lb .close").focus(); };
const closeLb = () => { lb.classList.remove("open"); document.body.style.overflow = ""; img.removeAttribute("src"); };
const step = (d) => { at = (at + d + list.length) % list.length; render(); };
document.addEventListener("click", (e) => { const a = e.target.closest("a.shot"); if (a && !e.metaKey && !e.ctrlKey) { e.preventDefault(); openLb(a); } });
$(".lb .close").addEventListener("click", closeLb);
$(".lb .prev").addEventListener("click", () => step(-1));
$(".lb .next").addEventListener("click", () => step(1));
img.addEventListener("click", () => lb.classList.toggle("zoom"));
lb.addEventListener("click", (e) => { if (e.target === lb || e.target.classList.contains("stage")) closeLb(); });
document.addEventListener("keydown", (e) => {
  if (!lb.classList.contains("open")) return;
  if (e.key === "Escape") closeLb(); else if (e.key === "ArrowLeft") step(-1); else if (e.key === "ArrowRight") step(1);
});
`;

const html = `<!doctype html>
<html lang="en-GB">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>App Store Review</title>
<meta name="description" content="Hammock for iPhone's App Store listing, every locale and asset, for review.">
<link rel="icon" href="${url(ICON)}">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600&family=JetBrains+Mono:wght@400;500&family=Space+Grotesk:wght@500;600;700&display=swap" rel="stylesheet">
<style>${CSS}</style>
</head>
<body>
<div class="top">
  <a class="brand" href="#overview"><span class="icon"><img src="${url(ICON)}" alt=""></span>Hammock <small>App Store review</small></a>
  <span class="spacer"></span>
  ${errors.length ? badge("error", plural(errors.length, "problem")) : warns.length ? badge("warn", plural(warns.length, "warning")) : badge("ok", "Ready")}
  <div class="theme" role="group" aria-label="Theme"><button data-theme="auto">Auto</button><button data-theme="light">Light</button><button data-theme="dark">Dark</button></div>
</div>
<div class="layout">
  <nav class="side">${SECTIONS.map(([id, label]) => `<a href="#${id}"><span>${label}</span>${countFor(id)}</a>`).join("")}</nav>
  <main>
${overview}
${listingSection}
${whatsNewSection}
${reviewSection}
${testflightSection}
${appInfoSection}
${privacySection}
${shotsSection}
${mkSection}
${videoSection}
${headerSection}
  </main>
</div>
<div class="lb" role="dialog" aria-modal="true" aria-label="Image viewer">
  <span class="n"></span>
  <div class="stage"><img alt=""></div>
  <div class="bar"><div><b></b><br><span></span></div><div class="meta"></div></div>
  <button class="close" aria-label="Close">×</button><button class="prev" aria-label="Previous">‹</button><button class="next" aria-label="Next">›</button>
</div>
<script>${JS}</script>
</body>
</html>
`;

writeFileSync(OUT, html);
console.log(`${relative(process.cwd(), OUT)}: ${plural(errors.length, "problem")}, ${plural(warns.length, "warning")}`);
for (const f of errors.slice(0, 8)) console.log(`  ✗ ${f.text}`);
if (errors.length > 8) console.log(`  … and ${errors.length - 8} more on the page`);

if (open) {
  const href = pathToFileURL(OUT).href;
  if (process.env.HMK_PANE_ID && spawnSync("hmk", ["browser", href], { stdio: "inherit" }).status === 0) process.exit(0);
  execFileSync("open", [href]);
}
