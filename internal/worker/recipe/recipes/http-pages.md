An http-pages feed is a set of web pages named by a sitemap or an HTML index. Each entry is one page
whose revision (its ETag, Last-Modified or content hash) was not seen before; its key is the URL path
and its locator the page URL, already given. The page is read by the built-in HTTP fetch, with no
connector.

A unit for it covers one page: its source is the page's own Source. Name the pages the unit feeds after
the page's subject, not after its path, and keep one unit per page. A page that disappears from the
set is never reported.
