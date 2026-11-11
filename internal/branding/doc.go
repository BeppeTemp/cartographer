// Package branding parses and judges the branding/ folder of a plugin's atlas
// side: one accent colour, a company logo (light and dark) and a favicon, all
// data. A strict declaration names the images, every file is judged by its
// bytes and caps, and an SVG is refused if it holds active content or
// references outside the file. It derives from the accent the dark and light
// theme tokens of the Atlas, verified against the contrast thresholds
// web/src/test/contrast.test.ts audits, and renders them as the text of a
// stylesheet. It imports only the standard library and yaml.
package branding
