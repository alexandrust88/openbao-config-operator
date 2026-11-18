---
description: Verify the OpenBao Config Operator documentation in Google Search Console, submit its sitemap and check indexing without changing stable URLs.
---

# Submit and monitor documentation indexing

Use this guide after the documentation has been deployed to GitHub Pages. You need a Google
account and permission to publish documentation. A successful deployment makes
pages available to crawlers; it does not guarantee indexing or a position in search results.

## Verify the published site

The documentation base URL is:

```text
https://docs.obco.dev/
```

`mkdocs.yml` sets this as the local build default. The Pages workflow supplies the deployed base
URL through `SITE_URL`, which MkDocs uses for canonical links and `sitemap.xml`. Keep the trailing
slash and project path. If the repository or domain moves, update links and redirects together.

After deployment, open the homepage, quickstart and API reference. Inspect the page source for:

- A descriptive title and description that match the page's content.
- One canonical link pointing to that page under the published base URL.
- No `noindex` instruction on a normal documentation page. Old redirect aliases deliberately
  retain `noindex` and point to their replacement pages.
- A successful response for [sitemap.xml](https://docs.obco.dev/sitemap.xml),
  containing the published documentation URLs.

Google reads `robots.txt` at the host root, which is now the site itself:
`https://docs.obco.dev/robots.txt`. A missing file returning 404 is not a crawl restriction, so the site
does not need one. See [Google's robots.txt rules](https://developers.google.com/crawling/docs/robots-txt/robots-txt-spec).

## Verify ownership in Search Console

1. Open [Google Search Console](https://search.google.com/search-console) with the account that
   should own this documentation property.
2. Add a **Domain property** for `obco.dev`. Search Console shows a `TXT` record; add it in Cloudflare
   (DNS, type `TXT`, name `@`) and select **Verify**. A domain property covers `docs.obco.dev` and any
   later subdomain, and needs no file in the repository. Keep the `TXT` record: removing it can cause
   ownership verification to expire.
3. If you cannot change DNS, add a **URL-prefix property** for exactly `https://docs.obco.dev/` instead,
   choose **HTML file upload**, put Google's verification file unchanged in `docs/` (MkDocs copies it to
   the site root), deploy through the Pages workflow and select **Verify**. Keep that file deployed.

Use the verification method offered for your property; do not invent a token or reuse somebody
else's. Google also supports HTML tag verification if file upload is unsuitable. Follow its
[ownership verification instructions](https://support.google.com/webmasters/answer/9008080).

## Submit the sitemap and inspect important pages

In the verified property's **Sitemaps** report, submit:

```text
https://docs.obco.dev/sitemap.xml
```

Use **URL Inspection** for the homepage and quickstart. Run the live test, resolve any fetch or
indexing exclusions it reports, then request indexing for those pages. A sitemap helps Google
discover the remaining pages; it is not a ranking guarantee. See [building and submitting a
sitemap](https://developers.google.com/search/docs/crawling-indexing/sitemaps/build-sitemap).

Crawling can take days to weeks, and indexing is not guaranteed. Repeated requests for the same
URL do not make it faster. Use the **Page indexing** report to investigate exclusions and URL
Inspection to compare Google's selected canonical with the declared canonical. See [requesting
a recrawl](https://developers.google.com/search/docs/crawling-indexing/ask-google-to-recrawl).

## Maintain useful search results

Use the **Performance** report's queries, impressions and clicks to identify questions the docs
can answer better. Improve the relevant existing page and link to it from the appropriate guide
or overview. Keep tutorials runnable, reference generated from the CRDs and compatibility claims
consistent with the implementation.

Write specific titles and descriptions for important pages. The theme appends the site name,
so a title names the task and OpenBao, not the full project name. Google may use page text
instead of the supplied description when constructing a result snippet. Avoid keyword lists,
duplicate articles, invented reviews and changing established URLs merely to add a search term.
See [title guidance](https://developers.google.com/search/docs/appearance/title-link),
[snippet guidance](https://developers.google.com/search/docs/appearance/snippet), and the
[SEO starter guide](https://developers.google.com/search/docs/fundamentals/seo-starter-guide).

Check the repository's **About** description and website link when the project scope changes.
Describe it as an OpenBao configuration operator for Kubernetes and GitOps. OpenBao is the
supported target; do not advertise Vault compatibility. Link the published documentation from
releases and chart metadata where it helps users find installation and configuration guidance.

## Keep release labels consistent

The README badge and Pages build select the highest semantic version among published,
non-prerelease GitHub releases. A tag alone is not a published release. The Pages workflow
passes that selection as `RELEASE_TAG`; local builds without it omit the release banner and
use version placeholders. To preview a published version locally, export its tag as
`RELEASE_TAG` before building. GitHub's manually selected **Latest** flag can
point at an older release, so it is not the source for these labels.
