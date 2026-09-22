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
}: {
  children: string;
  /** Called with a concept id when a [[wiki-link]] in the body is followed. */
  onNavigate?(id: string): void;
}) {
  return (
    <div className="markdown">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[rehypeSanitize]}
        components={{
          a({ href, children: text, ...props }) {
            const target = href?.startsWith(CONCEPT_HREF) ? decodeURIComponent(href.slice(CONCEPT_HREF.length)) : null;
            if (target !== null) {
              return (
                <a
                  {...props}
                  href={`?concept=${encodeURIComponent(target)}`}
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
            const external = !!href && /^[a-z][a-z0-9+.-]*:/i.test(href) && !href.startsWith("#");
            return (
              <a
                {...props}
                href={href}
                {...(external ? { target: "_blank", rel: "noopener noreferrer" } : {})}
              >
                {text}
                {external && (
                  <span aria-label=" (opens in a new tab)" className="markdown__external">
                    {" ↗"}
                  </span>
                )}
              </a>
            );
          },
          img({ alt, src }) {
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

/** The href a [[wiki-link]] is rewritten to before rendering, and recognised
 *  by in the link renderer. A relative path with the id percent-encoded: a
 *  fragment would be rewritten to #user-content-… by rehype-sanitize, and any
 *  ':' makes it read as a URL scheme, which sanitisation drops -- either way
 *  the link lost its href and was no longer a link. It is never followed as a
 *  URL: the renderer turns it into in-atlas navigation. */
const CONCEPT_HREF = "concept-link/";

const WIKILINK = /\[\[([^\]|#\n]+)(#[^\]|\n]*)?(?:\|([^\]\n]+))?\]\]/g;

/**
 * linkWikiLinks turns the KB's [[id]], [[id#section]] and [[id|label]] links
 * into Markdown links the renderer can make clickable. Code is left alone --
 * fenced blocks and inline spans show a wiki-link as the text it is -- and the
 * label defaults to the id, which is what the KB author wrote.
 */
export function linkWikiLinks(body: string): string {
  return body
    .split(/(```[\s\S]*?```|`[^`\n]*`)/)
    .map((part, i) =>
      i % 2 === 1
        ? part
        : part.replace(WIKILINK, (_m, id: string, _section: string | undefined, label: string | undefined) => {
            const text = (label ?? id).trim().replace(/[[\]]/g, "");
            return `[${text}](${CONCEPT_HREF}${encodeURIComponent(id.trim())})`;
          }),
    )
    .join("");
}
