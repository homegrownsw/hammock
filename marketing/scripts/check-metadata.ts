// Checks marketing/appstore/ against what App Store Connect accepts, offline (marketing/appstore/README.md):
// character limits, every locale having the same fields, keywords that don't waste their 100 characters, HTTPS
// URLs, the categories, the age rating answers, App Review's notes and TestFlight's text, and that nothing
// personal (the review contact) is in the tree. Placeholders still to be written ("[OWNER …]") are reported but
// don't fail the check; `just appstore-push` refuses them.
//
//   just appstore-check
import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = fileURLToPath(new URL("../appstore/", import.meta.url));
const METADATA = join(ROOT, "metadata");
const TESTFLIGHT = join(ROOT, "testflight");

/** App Store Connect's limits, in characters. */
const LIMITS: Record<string, number> = {
  "name.txt": 30,
  "subtitle.txt": 30,
  "promotional_text.txt": 170,
  "description.txt": 4000,
  "keywords.txt": 100,
  "privacy_url.txt": 255,
  "support_url.txt": 255,
  "marketing_url.txt": 255,
};
const REQUIRED = ["name.txt", "description.txt", "keywords.txt", "support_url.txt", "privacy_url.txt"];
const URLS = ["privacy_url.txt", "support_url.txt", "marketing_url.txt"];
/** Words App Review (and precheck) reject in a listing: other platforms' stores and unfinished software. */
const FORBIDDEN = [/\bandroid\b/i, /google play/i, /\bbeta\b/i, /\btestflight\b/i, /\blorem\b/i, /\bipod\b/i];
/** Other companies' product names: App Review rejects them as keywords (the description may name what works with Hammock). */
const TRADEMARKS = ["claude", "codex", "chatgpt", "openai", "anthropic", "gemini", "copilot", "cursor", "warp", "iterm", "ghostty"];
/** deliver's codes for the categories Hammock could be in. */
const CATEGORIES = ["DEVELOPER_TOOLS", "PRODUCTIVITY", "UTILITIES", "BUSINESS"];

const RATINGS = ["NONE", "INFREQUENT_OR_MILD", "FREQUENT_OR_INTENSE"];
const RATED = [
  "alcoholTobaccoOrDrugUseOrReferences", "contests", "gamblingSimulated", "gunsOrOtherWeapons",
  "horrorOrFearThemes", "matureOrSuggestiveThemes", "medicalOrTreatmentInformation", "profanityOrCrudeHumor",
  "sexualContentGraphicAndNudity", "sexualContentOrNudity", "violenceCartoonOrFantasy", "violenceRealistic",
  "violenceRealisticProlongedGraphicOrSadistic",
];
const YES_NO = [
  "advertising", "ageAssurance", "gambling", "healthOrWellnessTopics", "lootBox", "messagingAndChat",
  "parentalControls", "socialMedia", "socialMediaAgeRestricted", "unrestrictedWebAccess", "userGeneratedContent",
];

const errors: string[] = [];
const notes: string[] = [];
const fail = (msg: string) => errors.push(msg);

/** Characters as App Store Connect counts them (code points, not UTF-16 units or bytes). */
const length = (s: string) => [...s].length;
const read = (path: string) => readFileSync(path, "utf8").replace(/\n$/, "");
const dirs = (path: string) =>
  existsSync(path) ? readdirSync(path).filter((n) => statSync(join(path, n)).isDirectory()).sort() : [];
const files = (path: string) => readdirSync(path).filter((n) => n.endsWith(".txt")).sort();
const placeholder = (where: string, text: string) => {
  if (text.includes("[OWNER")) notes.push(`${where} still has an [OWNER …] placeholder`);
};

// --- Each locale's listing text.
const locales = dirs(METADATA).filter((n) => !n.startsWith("review_") && !n.startsWith("trade_"));
if (locales.length === 0) fail("metadata/ has no locale directories");
const fieldsOf = new Map<string, string[]>();
for (const locale of locales) {
  const dir = join(METADATA, locale);
  const names = files(dir);
  fieldsOf.set(locale, names);
  if (names.includes("release_notes.txt")) {
    fail(`metadata/${locale}/release_notes.txt: What's New comes from the release notes, never this file`);
  }
  for (const req of REQUIRED) if (!names.includes(req)) fail(`metadata/${locale}/ has no ${req}`);
  for (const name of names) {
    const where = `metadata/${locale}/${name}`;
    const text = read(join(dir, name));
    placeholder(where, text);
    const limit = LIMITS[name];
    if (limit && length(text) > limit) fail(`${where} is ${length(text)} characters, over ${limit}`);
    if (URLS.includes(name) && text && !/^https:\/\/\S+$/.test(text)) fail(`${where} isn't one https:// URL`);
    if (!URLS.includes(name)) {
      for (const re of FORBIDDEN) if (re.test(text)) fail(`${where} says "${text.match(re)![0]}", which App Review rejects`);
    }
  }
  const name = names.includes("name.txt") ? read(join(dir, "name.txt")) : "";
  const subtitle = names.includes("subtitle.txt") ? read(join(dir, "subtitle.txt")) : "";
  if (names.includes("keywords.txt")) {
    const kw = read(join(dir, "keywords.txt"));
    if (/,\s/.test(kw)) fail(`metadata/${locale}/keywords.txt: no spaces after commas (they count against the 100)`);
    const words = kw.split(",").map((k) => k.trim().toLowerCase()).filter(Boolean);
    const dupes = words.filter((w, i) => words.indexOf(w) !== i);
    if (dupes.length) fail(`metadata/${locale}/keywords.txt repeats ${[...new Set(dupes)].join(", ")}`);
    for (const w of words) {
      if (TRADEMARKS.some((t) => w.split(/\s+/).includes(t))) fail(`metadata/${locale}/keywords.txt: "${w}" is another company's product name`);
    }
    // The name and subtitle are searched anyway: a keyword repeating one of their words wastes the space.
    const taken = new Set(`${name} ${subtitle}`.toLowerCase().match(/[a-z0-9]+/g) ?? []);
    for (const w of words) {
      const hit = w.split(/\s+/).find((part) => taken.has(part.replace(/'s$/, "")));
      if (hit) fail(`metadata/${locale}/keywords.txt: "${w}" repeats "${hit}" from the name or subtitle`);
    }
  }
}
const [first, ...rest] = locales;
for (const locale of rest) {
  const a = fieldsOf.get(first)!.join(","), b = fieldsOf.get(locale)!.join(",");
  if (a !== b) fail(`metadata/${locale}/ has different fields from metadata/${first}/ (${b} vs ${a})`);
}

// --- The app-wide fields.
for (const [file, allowed] of [["primary_category.txt", CATEGORIES], ["secondary_category.txt", CATEGORIES]] as const) {
  const path = join(METADATA, file);
  const value = existsSync(path) ? read(path).trim() : "";
  if (file === "primary_category.txt" && !value) fail(`metadata/${file} is empty`);
  if (value && !allowed.includes(value)) fail(`metadata/${file}: ${value} isn't one of ${allowed.join(", ")}`);
}
if (!existsSync(join(METADATA, "copyright.txt"))) fail("metadata/ has no copyright.txt");

// --- App Review: the notes are here; the contact is personal data and must not be.
const review = join(METADATA, "review_information");
const reviewNotes = join(review, "notes.txt");
if (!existsSync(reviewNotes) || !read(reviewNotes).trim()) fail("metadata/review_information/notes.txt is missing or empty");
else if (length(read(reviewNotes)) > 4000) fail("metadata/review_information/notes.txt is over 4000 characters");
for (const personal of ["first_name.txt", "last_name.txt", "email_address.txt", "phone_number.txt"]) {
  if (existsSync(join(review, personal))) {
    fail(`metadata/review_information/${personal} is personal data: it belongs in marketing/.env.appstore, not git`);
  }
}

// --- TestFlight's test information.
const tfLocales = dirs(TESTFLIGHT);
if (tfLocales.length === 0) notes.push("testflight/ has no locales: a push leaves TestFlight's test information alone");
for (const locale of tfLocales) {
  const dir = join(TESTFLIGHT, locale);
  const desc = join(dir, "description.txt");
  if (!existsSync(desc) || !read(desc).trim()) fail(`testflight/${locale}/description.txt is missing or empty`);
  else if (length(read(desc)) > 4000) fail(`testflight/${locale}/description.txt is over 4000 characters`);
  for (const name of files(dir)) {
    const text = read(join(dir, name));
    placeholder(`testflight/${locale}/${name}`, text);
    if (name.endsWith("_url.txt") && text && !/^https:\/\/\S+$/.test(text)) fail(`testflight/${locale}/${name} isn't one https:// URL`);
  }
}

// --- The age rating questionnaire (deliver's app_rating_config_path).
const age = JSON.parse(readFileSync(join(ROOT, "age_rating.json"), "utf8")) as Record<string, unknown>;
for (const key of RATED) {
  if (!RATINGS.includes(age[key] as string)) fail(`age_rating.json: ${key} must be one of ${RATINGS.join(", ")}`);
}
for (const key of YES_NO) if (typeof age[key] !== "boolean") fail(`age_rating.json: ${key} must be true or false`);
for (const key of Object.keys(age)) {
  if (![...RATED, ...YES_NO].includes(key)) fail(`age_rating.json: ${key} isn't a question App Store Connect asks`);
}

for (const n of notes) console.log(`note: ${n}`);
if (errors.length) {
  for (const e of errors) console.error(`✗ ${e}`);
  console.error(`\n${errors.length} problem(s).`);
  process.exit(1);
}
console.log(`✓ marketing/appstore: ${locales.join(", ")} listing, TestFlight (${tfLocales.join(", ") || "none"}), age rating`);
