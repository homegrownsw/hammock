// Colour arithmetic shared by the pages (theme.js, files.js), with nothing
// else in it: files.js stays free of CodeMirror.

export function hexToRGB(h) {
  const m = /^#?([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})/i.exec(h || "")
  return m ? [parseInt(m[1], 16), parseInt(m[2], 16), parseInt(m[3], 16)] : [128, 128, 128]
}
export function mix(a, b, f) {
  const x = hexToRGB(a), y = hexToRGB(b)
  return "#" + x.map((v, i) => Math.round(v + (y[i] - v) * f).toString(16).padStart(2, "0")).join("")
}
// c at alpha f: over the background it looks like mix(background, c, f), but
// lets the selection (drawn beneath the text) show through.
export function tint(c, f) {
  const x = hexToRGB(c)
  return `rgba(${x[0]}, ${x[1]}, ${x[2]}, ${f})`
}
