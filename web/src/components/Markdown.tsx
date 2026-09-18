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
export function Markdown({ children }: { children: string }) {
  return (
    <div className="markdown">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[rehypeSanitize]}
        components={{
          a({ href, children: text, ...props }) {
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
        {children}
      </ReactMarkdown>
    </div>
  );
}
