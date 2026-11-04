import ReactMarkdown from "react-markdown";
import rehypeSanitize from "rehype-sanitize";
import remarkGfm from "remark-gfm";

/**
 * Concept bodies are KB content, and a KB is written by agents and by people.
 * It is rendered as untrusted input:
 *
 *  - raw HTML is never parsed (no rehype-raw), so a <script> or an onerror
 *    attribute in a body is text, not markup;
 *  - rehype-sanitize runs anyway, as defence in depth against a future plugin
 *    that reintroduces HTML;
 *  - external links open with noopener noreferrer and are marked, so a click
 *    leaving the atlas is a deliberate one;
 *  - images are not fetched. A remote image in a concept body would be the one
 *    network request this UI promises never to make, and an off-origin GET is
 *    a tracking pixel whether or not it was meant as one.
 */
export function Markdown({
  children,
  onNavigate,
  hrefOf,
  conceptId,
  outbound,
}: {
  children: string;
  /** Called with a concept id when a [[wiki-link]] or a relative `.md` link is followed. */
  onNavigate?(id: string): void;
  /** The address of a concept in this Atlas (kb and Map included), for
   *  open-in-new-tab and copy-link; without it the href carries the concept only. */
  hrefOf?(id: string): string;
  /** The concept the body belongs to: relative `.md` links resolve against it. */
  conceptId?: string;
  /** The ids the server resolved this body's links to; picks between the two
   *  bases an expanded concept's relative link can have (D149). */
  outbound?: readonly string[];
}) {
  return (
    <div className="markdown">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[rehypeSanitize]}
        components={{
          a({ node: _node, href, children: text, ...props }) {
            const target = href?.startsWith(CONCEPT_HREF)
              ? decodeConceptTarget(href.slice(CONCEPT_HREF.length))
              : conceptId !== undefined && href
                ? resolveConceptHref(href, conceptId, outbound)
                : null;
            // An undecodable wiki-link target is plain text: the rewritten href is
            // not a URL anyone can follow.
            if (target === null && href?.startsWith(CONCEPT_HREF)) return <span>{text}</span>;
            if (target !== null) {
              return (
                <a
                  {...props}
                  href={hrefOf ? hrefOf(target) : `?concept=${encodeURIComponent(target)}`}
                  className="markdown__wikilink"
                  onClick={(event) => {
                    if (!onNavigate) return;
                    event.preventDefault();
                    onNavigate(target);
                  }}
                >
                  {text}
                </a>
              );
            }
            // A protocol-relative "//host" leaves the origin as surely as a scheme does.
            const external = !!href && /^([a-z][a-z0-9+.-]*:|\/\/)/i.test(href);
            return (
              <a {...props} href={href} {...(external ? { target: "_blank", rel: "noopener noreferrer" } : {})}>
                {text}
                {external && (
                  <span aria-label=" (opens in a new tab)" className="markdown__external">
                    {" ↗"}
                  </span>
                )}
              </a>
            );
          },
          img({ node: _node, alt, src }) {
            return (
              <span className="markdown__image" role="img" aria-label={alt || "image"}>
                <span aria-hidden="true">image</span>
                <code>{alt || src || "unnamed"}</code>
              </span>
            );
          },
        }}
      >
        {linkWikiLinks(children)}
      </ReactMarkdown>
    </div>
  );
}

/** The id a rewritten wiki-link carries. A body is author-written text, so the
 *  percent-escapes may be malformed or not UTF-8 (`%FF`, a lone `%C3`):
 *  decoding must never throw during render. Such a link is not a concept link,
 *  and falls back to the plain link text (null). */
function decodeConceptTarget(encoded: string): string | null {
  try {
    return decodeURIComponent(encoded);
  } catch {
    return null;
  }
}

/** The href a [[wiki-link]] is rewritten to before rendering, and recognised
 *  by in the link renderer. A relative path with the id percent-encoded: a
 *  fragment would be rewritten to #user-content-… by rehype-sanitize, and any
 *  ':' makes it read as a URL scheme, which sanitisation drops -- either way
 *  the link lost its href and was no longer a link. It is never followed as a
 *  URL: the renderer turns it into in-atlas navigation. */
const CONCEPT_HREF = "concept-link/";

/**
 * linkWikiLinks turns the KB's [[id]], [[id#section]] and [[id|label]] links
 * into Markdown links the renderer can make clickable. Code is left alone --
 * fenced blocks and inline spans show a wiki-link as the text it is -- and the
 * label defaults to the id, which is what the KB author wrote.
 */
export function linkWikiLinks(body: string): string {
  return body.replace(CODE_OR_WIKILINK, (m, _fence, _fenceChar, _ticks, id: string | undefined, _section, label: string | undefined) => {
    if (id === undefined) return m;
    const text = (label ?? id).trim().replace(/[[\]]/g, "");
    return `[${text}](${CONCEPT_HREF}${encodeURIComponent(id.trim())})`;
  });
}

/** A fenced block (``` or ~~~, closed by the same fence or the end), an inline
 *  span (a backtick run closed by a run of the same length), or a wiki-link:
 *  the first two are matched only to be left alone, as the server's graph sees
 *  no link inside code (D150). */
const CODE_OR_WIKILINK = new RegExp(
  // The closing fence repeats the opener's character at least as long (CommonMark): \\2 is the
    // character, \\1 the whole opener, and `\\2*` lets the closer run longer.
  "^ {0,3}((`|~)\\2{2,})[^\\n]*\\n(?:[\\s\\S]*?\\n {0,3}\\1\\2*[ \\t]*(?=\\n|$)|[\\s\\S]*)" +
    // An inline span never crosses a blank line: a paragraph break ends it unclosed.
    "|(`+)(?!`)(?:(?!\\n[ \\t]*\\n)[\\s\\S])*?(?<!`)\\3(?!`)" +
    "|\\[\\[([^\\]|#\\n]+)(#[^\\]|\\n]*)?(?:\\|([^\\]\\n]+))?\\]\\]",
  "gm",
);

/**
 * The concept a relative Markdown link points at, or null when it is not one:
 * a scheme, a `//host`, a `#fragment` or anything that does not end in `.md`
 * stays a plain link. `/x.md` is root-relative; the others resolve against
 * the directory of the file holding the link. An expanded concept `m/c` lives
 * in `m/c/index.md`, so its links have two possible bases; the one the server
 * resolved (`outbound`) wins, else the id's directory.
 */
export function resolveConceptHref(href: string, id: string, outbound: readonly string[] = []): string | null {
  if (/^([a-z][a-z0-9+.-]*:|\/\/|#)/i.test(href)) return null;
  let path = href.replace(/[?#].*$/, "");
  try {
    path = decodeURIComponent(path);
  } catch {
    // a malformed % stays as written
  }
  if (!path.endsWith(".md")) return null;
  const join = (base: string[]) => {
    const parts = path.startsWith("/") ? [] : base;
    const out = [...parts];
    for (const seg of path.split("/")) {
      if (seg === "" || seg === ".") continue;
      if (seg === "..") out.pop();
      else out.push(seg);
    }
    const last = out.at(-1)!;
    out[out.length - 1] = last.slice(0, -3);
    if (out.at(-1) === "index" && out.length > 1) out.pop();
    return out.join("/");
  };
  const dir = id.split("/").slice(0, -1);
  const candidates = [join(dir), join(id.split("/"))];
  return candidates.find((c) => outbound.includes(c)) ?? candidates[0]!;
}
