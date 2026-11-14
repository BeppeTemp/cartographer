// Package branding parses and judges the branding/ folder of a plugin's atlas
// side: a company logo (light and dark) and a company name, all data.
// A branding.yaml that declares an accent or a favicon is refused: the branding
// changes no colour and the tab keeps Cartographer's favicon. A strict declaration names the images, every file is judged by its
// bytes and caps, and an SVG is refused if it holds active content or
// references outside the file. It imports only the standard library and yaml: internal/plugin/core
// carries its Spec and internal/pluginserver loads it.
package branding
