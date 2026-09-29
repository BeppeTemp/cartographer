/**
 * Many KB templates end a page with a hand-written "Collegamenti" (Links)
 * section: useful to whoever reads the Markdown outside Cartographer, a
 * duplicate of the Links tab inside it. withoutRedundantLinksSection drops
 * that section from the rendered body -- only when it is nothing but links
 * and every one of them is already among the concept's outbound links, so no
 * authored word and no link the tab would miss is ever hidden.
 */

const HEADINGS = /^(collegamenti|links|related|see also|vedi anche)$/i;

export function withoutRedundantLinksSection(body: string, conceptId: string, outbound: string[]): string {
  const lines = body.split("\n");
  const known = new Set(outbound);
  let start = -1;
  let level = 0;
  for (let i = 0; i < lines.length; i++) {
    const m = /^(#{2,6})\s+(.+?)\s*#*\s*$/.exec(lines[i]!);
    if (m && HEADINGS.test(m[2]!.trim())) {
      start = i;
      level = m[1]!.length;
      break;
    }
  }
  if (start === -1) return body;
  let end = lines.length;
  for (let i = start + 1; i < lines.length; i++) {
    const m = /^(#{1,6})\s/.exec(lines[i]!);
    if (m && m[1]!.length <= level) {
      end = i;
      break;
    }
  }
  const section = lines.slice(start + 1, end).join("\n");
  const targets: string[] = [];
  let rest = section.replace(/\[\[([^\]|#]+)(?:#[^\]|]*)?(?:\|[^\]]*)?\]\]/g, (_, id: string) => {
    targets.push(id.trim());
    return "";
  });
  rest = rest.replace(/\[[^\]]*\]\(([^)\s]+)\)/g, (whole, href: string) => {
    if (/^[a-z][a-z0-9+.-]*:/i.test(href)) {
      targets.push("\u0000external");
      return whole;
    }
    targets.push(href);
    return "";
  });
  // Anything left besides list markers and punctuation is authored text.
  if (/[\p{L}\p{N}]/u.test(rest) || targets.length === 0) return body;
  const covered = targets.every((t) => candidates(conceptId, t).some((id) => known.has(id)));
  if (!covered) return body;
  return [...lines.slice(0, start), ...lines.slice(end)].join("\n").replace(/\n+$/, "\n");
}

/** The concept ids a link target may mean: a wiki id as written, or a
 *  relative .md path resolved from the concept's directory -- for a concept
 *  stored flat (id.md) and expanded (id/index.md) alike. */
function candidates(conceptId: string, target: string): string[] {
  const clean = target.split("#")[0]!.replace(/\.md$/, "").replace(/\/index$/, "");
  if (!clean.startsWith(".") && !clean.includes("/..")) return [clean, clean.replace(/^\//, "")];
  const parent = conceptId.includes("/") ? conceptId.slice(0, conceptId.lastIndexOf("/")) : "";
  return [resolve(parent, clean), resolve(conceptId, clean)];
}

function resolve(base: string, rel: string): string {
  const parts = base ? base.split("/") : [];
  for (const seg of rel.split("/")) {
    if (seg === "" || seg === ".") continue;
    if (seg === "..") parts.pop();
    else parts.push(seg);
  }
  return parts.join("/");
}
