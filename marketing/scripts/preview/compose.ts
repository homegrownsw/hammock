// Cuts the App Store's videos from the takes (marketing/appstore/README.md, "The app preview"): the edit in
// marketing/appstore/preview.json, laid out twice, in the marketing images' colours and type.
//
//   app preview      886 x 1920: the caption above the screen; 30 fps, H.264, a silent sound track (Apple
//                    wants one). previews/<locale>/01-hammock-preview.mp4, and its poster frame beside it.
//   search results   2400 x 1600 (3:2): the caption beside the screen; 30 fps, no sound (it's always muted).
//                    creative/<locale>/search-results.mp4
//
// Playwright renders each shot's page (the ground and the caption) and a mask of the screen's window (rounded
// corners); ffmpeg puts the take's footage through that window, crossfades the shots, adds the end card and
// encodes. In: marketing/build/takes/<take>.{mov,json} (marketing/scripts/appstore-preview-video). GENERATED.
import { execFileSync } from "node:child_process";
import { copyFileSync, existsSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { chromium } from "@playwright/test";

const ROOT = fileURLToPath(new URL("../../../", import.meta.url));
const APPSTORE = join(ROOT, "marketing/appstore");
const TAKES = join(ROOT, "marketing/build/takes");
const WORK = join(ROOT, "marketing/build/preview");
const FPS = 30;
const FADE = 0.3;
const LOCALES = readdirSync(join(APPSTORE, "metadata"), { withFileTypes: true })
  .filter((d) => d.isDirectory() && !d.name.startsWith("review_") && !d.name.startsWith("trade_"))
  .map((d) => d.name)
  .sort();

interface Shot { take: string; mark: string; from: number; seconds: number; caption: string }
const edit = JSON.parse(readFileSync(join(APPSTORE, "preview.json"), "utf8")).edit as {
  poster: number;
  shots: Shot[];
  end: { seconds: number; headline: string; subline: string };
};

/** Each video's canvas, where the screen goes in it, and where and how big its copy is. */
const LAYOUTS = {
  preview: {
    w: 886, h: 1920, screen: { x: 113, y: 372, w: 660 }, radius: 52,
    copy: "left:40px; right:40px; top:80px; text-align:center;", headline: 70, endHeadline: 84, subline: 34,
  },
  search: {
    // Inside Apple's art safe area (its templates: 56.5% x 40.2% of the canvas, centred; 1355 x 644 here): the caption
    // and the screen, which overhangs it a little at the top and bottom (status bar and tab bar only).
    w: 2400, h: 1600, screen: { x: 1500, y: 430, w: 341 }, radius: 30,
    copy: "left:560px; width:880px; top:50%; transform:translateY(-50%);", headline: 88, endHeadline: 92, subline: 38,
  },
} as const;
type Kind = keyof typeof LAYOUTS;

const esc = (s: string) => s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]!);
const lines = (s: string) => s.split("\n").map((l) => `<span>${esc(l).replace(/\*([^*]+)\*/g, "<em>$1</em>")}</span>`).join("");
// The iPhone app's icon, drawn as Icon Composer does (apple/iOS/Hammock/AppIcon.icon): its fill under its mark.
const mark = pathToFileURL(join(ROOT, "apple/iOS/Hammock/AppIcon.icon/Assets/mark.png")).href;

function pageHTML(kind: Kind, body: string, extra = ""): string {
  const L = LAYOUTS[kind];
  return `<!doctype html><html><head><meta charset="utf-8">
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500&family=Space+Grotesk:wght@700&display=block" rel="stylesheet">
<style>
  html, body { margin:0; width:${L.w}px; height:${L.h}px; overflow:hidden; background:#06140e; -webkit-font-smoothing:antialiased; }
  body { position:relative; background-image: radial-gradient(70% 45% at 50% 0%, rgba(46,138,98,.55), transparent 70%),
    radial-gradient(60% 40% at 90% 85%, rgba(10,132,255,.10), transparent 70%); }
  .copy { position:absolute; ${L.copy} }
  h1 { margin:0; font:700 ${L.headline}px/1.04 "Space Grotesk", sans-serif; letter-spacing:-0.035em; color:#eaf6ef; }
  h1 span { display:block; }
  em { font-style:normal; background:linear-gradient(90deg,#61d836,#ffd60a 22%,#ff9f0a 40%,#ff453a 58%,#bf5af2 78%,#0a84ff);
    -webkit-background-clip:text; background-clip:text; color:transparent; }
  p { margin:${Math.round(L.subline * 0.7)}px 0 0; font:400 ${L.subline}px/1.35 Inter, sans-serif; color:#a3c2b2; }
  ${extra}
</style></head><body>${body}</body></html>`;
}

function maskHTML(kind: Kind): string {
  const L = LAYOUTS[kind], s = L.screen, h = Math.round((s.w * 2868) / 1320);
  return `<!doctype html><html><body style="margin:0;width:${L.w}px;height:${L.h}px;background:#000">
<div style="position:absolute;left:${s.x}px;top:${s.y}px;width:${s.w}px;height:${h}px;border-radius:${L.radius}px;background:#fff"></div></body></html>`;
}

function ffmpeg(args: string[]) {
  execFileSync("ffmpeg", ["-v", "error", "-y", ...args], { stdio: ["ignore", "ignore", "inherit"] });
}

// --- The takes' marks.
const marks = new Map<string, Record<string, number>>();
for (const shot of edit.shots) {
  if (marks.has(shot.take)) continue;
  const path = join(TAKES, `${shot.take}.json`);
  if (!existsSync(path)) throw new Error(`no take ${shot.take} (run just appstore-preview-video)`);
  marks.set(shot.take, JSON.parse(readFileSync(path, "utf8")).marks);
}

rmSync(WORK, { recursive: true, force: true });
mkdirSync(WORK, { recursive: true });
const browser = await chromium.launch();
const render = async (kind: Kind, html: string, out: string, transparent = false) => {
  const L = LAYOUTS[kind];
  const ctx = await browser.newContext({ viewport: { width: L.w, height: L.h } });
  const page = await ctx.newPage();
  const file = join(WORK, "page.html");
  writeFileSync(file, html);
  await page.goto(pathToFileURL(file).href, { waitUntil: "networkidle" });
  await page.evaluate(() => document.fonts.ready);
  await page.screenshot({ path: out, omitBackground: transparent });
  await ctx.close();
};

const made: Record<Kind, string> = { preview: "", search: "" };
for (const kind of Object.keys(LAYOUTS) as Kind[]) {
  const L = LAYOUTS[kind], s = L.screen, sh = Math.round((s.w * 2868) / 1320);
  const mask = join(WORK, `${kind}-mask.png`);
  await render(kind, maskHTML(kind), mask);
  const parts: { file: string; seconds: number }[] = [];
  for (const [i, shot] of edit.shots.entries()) {
    const at = marks.get(shot.take)![shot.mark];
    if (at === undefined) throw new Error(`take ${shot.take} has no mark ${shot.mark}`);
    const page = join(WORK, `${kind}-${i}.png`);
    await render(kind, pageHTML(kind, `<div class="copy"><h1>${lines(shot.caption)}</h1></div>`), page);
    const out = join(WORK, `${kind}-${i}.mp4`);
    // The footage, scaled into its window on a canvas the page's size, cut out by the mask, over the page.
    ffmpeg([
      "-loop", "1", "-framerate", String(FPS), "-i", page,
      "-ss", String(Math.max(0, at + shot.from)), "-t", String(shot.seconds), "-i", join(TAKES, `${shot.take}.mov`),
      "-loop", "1", "-framerate", String(FPS), "-i", mask,
      "-filter_complex",
      `[1:v]fps=${FPS},scale=${s.w}:${sh},setsar=1,pad=${L.w}:${L.h}:${s.x}:${s.y}:black[v];` +
        `[2:v]format=gray[m];[v][m]alphamerge[cut];[0:v][cut]overlay=format=auto,format=yuv420p`,
      "-t", String(shot.seconds), "-r", String(FPS), "-c:v", "libx264", "-crf", "16", "-preset", "fast", "-an", out,
    ]);
    parts.push({ file: out, seconds: shot.seconds });
  }
  // The end card: the icon, a headline and a subline, on the same ground.
  const endPng = join(WORK, `${kind}-end.png`);
  const iconSize = kind === "preview" ? 220 : 170;
  await render(kind, pageHTML(kind,
    `<div class="end"><span class="icon"><img src="${mark}"></span><h1>${lines(edit.end.headline)}</h1><p>${esc(edit.end.subline)}</p></div>`,
    `.end { position:absolute; left:0; right:0; top:50%; transform:translateY(-50%); text-align:center; padding:0 60px; }
     .end .icon { display:inline-block; width:${iconSize}px; height:${iconSize}px; border-radius:${Math.round(iconSize * 0.225)}px; overflow:hidden;
       margin-bottom:${Math.round(iconSize * 0.25)}px; background:linear-gradient(180deg, rgb(29,96,70), rgb(7,26,18));
       box-shadow:0 30px 80px rgba(0,0,0,.5), inset 0 0 0 1px rgba(255,255,255,.08); }
     .end .icon img { width:100%; height:100%; display:block; }
     .end h1 { font-size:${L.endHeadline}px; }`), endPng);
  const endMp4 = join(WORK, `${kind}-end.mp4`);
  ffmpeg(["-loop", "1", "-framerate", String(FPS), "-i", endPng, "-t", String(edit.end.seconds), "-r", String(FPS),
    "-vf", "format=yuv420p", "-c:v", "libx264", "-crf", "16", "-preset", "fast", endMp4]);
  parts.push({ file: endMp4, seconds: edit.end.seconds });

  // The shots, crossfaded in turn.
  const inputs = parts.flatMap((p) => ["-i", p.file]);
  let chain = "", last = "[0:v]", t = 0;
  for (let k = 1; k < parts.length; k++) {
    t += parts[k - 1].seconds - FADE;
    const out = k === parts.length - 1 ? "[out]" : `[x${k}]`;
    chain += `${last}[${k}:v]xfade=transition=fade:duration=${FADE}:offset=${t.toFixed(3)}${out};`;
    last = `[x${k}]`;
  }
  const total = parts.reduce((a, p) => a + p.seconds, 0) - FADE * (parts.length - 1);
  const final = join(WORK, `${kind}.mp4`);
  const audio = kind === "preview" ? ["-f", "lavfi", "-t", total.toFixed(3), "-i", "anullsrc=channel_layout=stereo:sample_rate=44100"] : [];
  ffmpeg([...inputs, ...audio, "-filter_complex", chain.replace(/;$/, ""), "-map", "[out]",
    ...(kind === "preview" ? ["-map", `${parts.length}:a`, "-c:a", "aac", "-b:a", "128k", "-shortest"] : ["-an"]),
    "-r", String(FPS), "-c:v", "libx264", "-profile:v", "high", "-pix_fmt", "yuv420p", "-crf", "18", "-maxrate", "12M",
    "-bufsize", "24M", "-movflags", "+faststart", final]);
  made[kind] = final;
  console.log(`  ${kind}: ${total.toFixed(1)} s`);
}
await browser.close();

// --- Into each listing locale (the app's words are English only: the same videos), with a stamp of what made them.
const stamp = `edit=${execFileSync("shasum", ["-a", "256", join(APPSTORE, "preview.json")]).toString().split(" ")[0]}\n` +
  `commit=${execFileSync("git", ["rev-parse", "--short", "HEAD"], { cwd: ROOT }).toString().trim()}\n`;
for (const loc of LOCALES) {
  const pv = join(APPSTORE, "previews", loc), cr = join(APPSTORE, "creative", loc);
  mkdirSync(pv, { recursive: true });
  mkdirSync(cr, { recursive: true });
  copyFileSync(made.preview, join(pv, "01-hammock-preview.mp4"));
  copyFileSync(made.search, join(cr, "search-results.mp4"));
  writeFileSync(join(pv, ".made"), stamp);
  writeFileSync(join(cr, ".made"), stamp);
  console.log(`  marketing/appstore/previews/${loc}/01-hammock-preview.mp4, creative/${loc}/search-results.mp4`);
}
// The poster frame, to look at (the push tells Apple its time).
ffmpeg(["-ss", String(edit.poster), "-i", made.preview, "-frames:v", "1", join(APPSTORE, "previews", "poster.png")]);
