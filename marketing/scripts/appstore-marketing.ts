// Composes the App Store marketing images from the screenshots (marketing/appstore/README.md, "The marketing
// images"): each screenshot framed in its device (frames-cli) and set under a headline and a subline, in the
// website's colours and type, then rendered by Playwright's Chromium at exactly the size App Store Connect takes.
// Every panel's copy and theme, and each device's layout, is in marketing/appstore/marketing.json; this only draws
// what that says.
//
//   just appstore-marketing                    # every device and panel, and a contact sheet of each set
//   just appstore-marketing --only allow       # one panel (by id)
//   just appstore-marketing --device ipad      # one device: iphone, ipad or duo (repeatable)
//
// In:   marketing/appstore/screenshots{,-dark}/en-GB/        (just appstore-screenshots): the iPhone and iPad sets
//       marketing/appstore/duo/screenshots{,-dark}/en-GB/    the iPhone Duo's (unfolded, landscape)
// Out:  marketing/appstore/marketing/<locale>/NN-<id>-<tag>.png, opaque, what deliver uploads (iPhone and iPad);
//       marketing/appstore/duo/marketing/<locale>/NN-<id>-duo.png, which the push uploads itself; each folder's
//       .captured, the screenshots' stamp; and preview-<tag>.png, each set side by side, to look at.
// The app's words are English only, so every listing locale gets the same images. All GENERATED and gitignored.
// The frames come from frames-cli (FRAMES_BIN, else `frames` on the PATH).
import { execFileSync } from "node:child_process";
import { copyFileSync, existsSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { chromium } from "@playwright/test";

const ROOT = fileURLToPath(new URL("../../", import.meta.url));
const APPSTORE = join(ROOT, "marketing/appstore");
const BUILD = join(ROOT, "marketing/build/marketing");
const CAPTURE_LOCALE = "en-GB";
const LOCALES = readdirSync(join(APPSTORE, "metadata"), { withFileTypes: true })
  .filter((d) => d.isDirectory() && !d.name.startsWith("review_") && !d.name.startsWith("trade_"))
  .map((d) => d.name)
  .sort();

type DeviceId = "iphone" | "ipad" | "duo";
const DEVICES: Record<DeviceId, { w: number; h: number; tag: string; shots: string; out: string }> = {
  iphone: { w: 1320, h: 2868, tag: "iphone69", shots: "screenshots", out: "marketing" },
  ipad: { w: 2064, h: 2752, tag: "ipad13", shots: "screenshots", out: "marketing" },
  duo: { w: 2853, h: 2007, tag: "duo", shots: "duo/screenshots", out: "duo/marketing" },
};

interface Panel { id: string; shot: string; theme: "light" | "dark"; headline: string; subline: string }
interface Layout {
  layout: "stacked" | "beside";
  headline: number;
  subline: number;
  copyTop?: number;
  copyLeft?: number;
  copyWidth: number;
  deviceWidth: number;
  deviceTop?: number;
  deviceRight?: number;
  /** frames-cli's colour for the frame on a dark or a light panel (none: its only one). */
  frame?: { dark: string; light: string };
  /** frames-cli's frame by name, where the screenshot's size matches more than one device's. */
  frameDevice?: string;
}
const data = JSON.parse(readFileSync(join(APPSTORE, "marketing.json"), "utf8")) as {
  panels: Panel[];
  devices: Record<DeviceId, Layout>;
};

// --- Arguments.
const args = process.argv.slice(2);
let only = "";
const devices: DeviceId[] = [];
for (let i = 0; i < args.length; i++) {
  if (args[i] === "--only") only = args[++i] ?? "";
  else if (args[i] === "--device") {
    const d = args[++i] as DeviceId;
    if (!(d in DEVICES)) throw new Error(`--device takes iphone, ipad or duo, not '${d}'`);
    devices.push(d);
  } else throw new Error(`unknown argument ${args[i]}`);
}
if (only && !data.panels.some((p) => p.id === only)) {
  throw new Error(`'${only}' isn't a panel: ${data.panels.map((p) => p.id).join(", ")}`);
}
const which: DeviceId[] = devices.length ? devices : ["iphone", "ipad", "duo"];
const frames = process.env.FRAMES_BIN ?? "frames";

// --- The page: the website's colours and type (marketing/index.html), a glow, the copy and the framed device.
const THEMES = {
  dark: {
    bg: "#06140e",
    glow: "radial-gradient(70% 45% at 50% 0%, rgba(46,138,98,.55), transparent 70%), radial-gradient(60% 40% at 90% 85%, rgba(10,132,255,.10), transparent 70%)",
    text: "#eaf6ef",
    sub: "#a3c2b2",
    accent: "linear-gradient(90deg, #61d836, #ffd60a 22%, #ff9f0a 40%, #ff453a 58%, #bf5af2 78%, #0a84ff)",
  },
  light: {
    bg: "#f3f8f4",
    glow: "radial-gradient(70% 45% at 50% 0%, rgba(61,255,162,.28), transparent 70%), radial-gradient(60% 40% at 10% 90%, rgba(191,90,242,.08), transparent 70%)",
    text: "#06140e",
    sub: "#3f5c4d",
    accent: "linear-gradient(90deg, #1f9d5a, #0f8f8a 45%, #0a6fd6 75%, #7a3fd0)",
  },
};

const esc = (s: string) => s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]!);
const headlineHTML = (s: string) =>
  s.split("\n").map((line) => `<span class="line">${esc(line).replace(/\*([^*]+)\*/g, '<em>$1</em>')}</span>`).join("");

function page(dev: DeviceId, panel: Panel, framed: string): string {
  const d = DEVICES[dev], l = data.devices[dev], t = THEMES[panel.theme];
  const copy = l.layout === "stacked"
    ? `left:${(d.w - l.copyWidth) / 2}px; top:${l.copyTop}px; width:${l.copyWidth}px; text-align:center;`
    : `left:${l.copyLeft}px; top:50%; transform:translateY(-50%); width:${l.copyWidth}px; text-align:left;`;
  const device = l.layout === "stacked"
    ? `left:${(d.w - l.deviceWidth) / 2}px; top:${l.deviceTop}px; width:${l.deviceWidth}px;`
    : `right:${l.deviceRight}px; top:50%; transform:translateY(-50%); width:${l.deviceWidth}px;`;
  return `<!doctype html><html><head><meta charset="utf-8">
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500&family=Space+Grotesk:wght@700&display=block" rel="stylesheet">
<style>
  html, body { margin:0; width:${d.w}px; height:${d.h}px; overflow:hidden; background:${t.bg}; }
  body { background-image:${t.glow}; position:relative; -webkit-font-smoothing:antialiased; }
  .copy { position:absolute; ${copy} }
  h1 { margin:0; font:700 ${l.headline}px/1.04 "Space Grotesk", sans-serif; letter-spacing:-0.035em; color:${t.text}; }
  h1 .line { display:block; }
  h1 em { font-style:normal; background:${t.accent}; -webkit-background-clip:text; background-clip:text; color:transparent; }
  p { margin:${Math.round(l.subline * 0.75)}px 0 0; font:400 ${l.subline}px/1.38 Inter, sans-serif; color:${t.sub}; text-wrap:balance; }
  img { position:absolute; ${device} filter:drop-shadow(0 40px 80px rgba(0,0,0,${panel.theme === "dark" ? ".55" : ".22"})); }
</style></head><body>
  <div class="copy"><h1>${headlineHTML(panel.headline)}</h1><p>${esc(panel.subline)}</p></div>
  <img src="${pathToFileURL(framed).href}">
</body></html>`;
}

// --- Each device and panel.
const browser = await chromium.launch();
const problems: string[] = [];
let made = 0;
for (const dev of which) {
  const d = DEVICES[dev];
  const ctx = await browser.newContext({ viewport: { width: d.w, height: d.h }, deviceScaleFactor: 1 });
  const tab = await ctx.newPage();
  const work = join(BUILD, dev);
  mkdirSync(work, { recursive: true });
  const outs = LOCALES.map((loc) => join(APPSTORE, d.out, loc));
  for (const o of outs) mkdirSync(o, { recursive: true });
  for (const [i, panel] of data.panels.entries()) {
    if (only && panel.id !== only) continue;
    const dir = join(APPSTORE, panel.theme === "dark" ? `${d.shots}-dark` : d.shots, CAPTURE_LOCALE);
    const shot = join(dir, `${panel.shot}-${d.tag}.png`);
    if (!existsSync(shot)) {
      problems.push(`${dev}: no ${shot.replace(ROOT, "")} (run just appstore-screenshots)`);
      continue;
    }
    // frames-cli writes <name>_framed.png into the folder it's given.
    const framedDir = join(work, "framed");
    rmSync(framedDir, { recursive: true, force: true });
    const colour = data.devices[dev].frame?.[panel.theme];
    const named = data.devices[dev].frameDevice;
    execFileSync(frames, ["frame", shot, "-o", framedDir, ...(named ? ["--device", named] : []), ...(colour ? ["--color", colour] : [])], { stdio: "ignore" });
    const framed = join(framedDir, `${panel.shot}-${d.tag}_framed.png`);
    // From a file, so the page may show the framed image beside it (a page made from a string can't).
    const html = join(work, "page.html");
    writeFileSync(html, page(dev, panel, framed));
    await tab.goto(pathToFileURL(html).href, { waitUntil: "networkidle" });
    await tab.evaluate(() => document.fonts.ready);
    // A headline that wraps onto more lines than it's written on, or a subline over two, doesn't fit its device.
    const fit = await tab.evaluate(() => {
      const h1lh = parseFloat(getComputedStyle(document.querySelector("h1")!).lineHeight);
      const lines = [...document.querySelectorAll("h1 .line")].map((el) => Math.round(el.getBoundingClientRect().height / h1lh));
      const p = document.querySelector("p")!;
      const lh = parseFloat(getComputedStyle(p).lineHeight);
      return { wrapped: lines.some((n) => n > 1), sublines: Math.round(p.getBoundingClientRect().height / lh) };
    });
    if (fit.wrapped) problems.push(`${dev} ${panel.id}: the headline wraps; shorten a line or make it smaller`);
    if (fit.sublines > 2) problems.push(`${dev} ${panel.id}: the subline takes ${fit.sublines} lines (two at most)`);
    const name = `${String(i + 1).padStart(2, "0")}-${panel.id}-${d.tag}.png`;
    const raw = join(work, name);
    await tab.screenshot({ path: raw, type: "png" });
    // App Store Connect takes no alpha: flattened to opaque RGB.
    const first = join(outs[0], name);
    execFileSync("ffmpeg", ["-v", "error", "-y", "-i", raw, "-pix_fmt", "rgb24", first]);
    for (const o of outs.slice(1)) copyFileSync(first, join(o, name));
    made++;
    console.log(`  ${first.replace(ROOT, "")}`);
  }
  // The screenshots' stamp, so the push can tell what these were made from.
  const stamp = join(APPSTORE, d.shots, CAPTURE_LOCALE, ".captured");
  if (existsSync(stamp)) for (const o of outs) copyFileSync(stamp, join(o, ".captured"));
  // Each set side by side, to look at.
  const set = readdirSync(outs[0]).filter((n) => n.endsWith(`-${d.tag}.png`)).sort();
  if (set.length) {
    const sheet = join(APPSTORE, d.out, `preview-${d.tag}.png`);
    const inputs = set.flatMap((n) => ["-i", join(outs[0], n)]);
    const scaled = set.map((_, k) => `[${k}:v]scale=-2:${dev === "duo" ? 600 : 900}[s${k}]`).join(";");
    const stack = `${set.map((_, k) => `[s${k}]`).join("")}hstack=inputs=${set.length}`;
    execFileSync("ffmpeg", ["-v", "error", "-y", ...inputs, "-filter_complex", set.length > 1 ? `${scaled};${stack}` : scaled.replace(/\[s0\]$/, ""), sheet]);
  }
  await ctx.close();
}
await browser.close();

if (problems.length) {
  for (const p of problems) console.error(`✗ ${p}`);
  process.exit(1);
}
console.log(`Made ${made} marketing image(s) for ${LOCALES.join(", ")} (${which.join(", ")}).`);
