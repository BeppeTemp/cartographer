/**
 * Where the docs live: the repository's blob URL on main, the same origin the
 * top bar's demo link already points at. The server hands out repository
 * paths ("docs/sync.md#agents-and-hooks"), never URLs: what a link goes to is
 * decided here, not by a response.
 */
const DOCS_ROOT = "https://github.com/BeppeTemp/cartographer/blob/main/";

/** docs/<dir>/<file>.md#<anchor>: no scheme, no host, no ".." segment. */
const DOC_PATH = /^docs\/(?:[\w-]+\/)*[\w.-]+\.md#[\w-]+$/;

export function docsUrl(doc: string): string | null {
  return DOC_PATH.test(doc) ? DOCS_ROOT + doc : null;
}

/** A "Docs" link to the page that explains a kind; nothing when the path is not one of ours. */
export function DocsLink({ doc }: { doc: string }) {
  const href = docsUrl(doc);
  if (!href) return null;
  return (
    <a href={href} target="_blank" rel="noopener noreferrer">
      Docs
    </a>
  );
}
