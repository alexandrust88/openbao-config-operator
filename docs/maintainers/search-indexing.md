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
https://alexandrust88.github.io/openbao-config-operator/
```

`mkdocs.yml` sets this as the local build default. The Pages workflow supplies the deployed base
URL through `SITE_URL`, which MkDocs uses for canonical links and `sitemap.xml`. Keep the trailing
slash and project path. If the repository or domain moves, update links and redirects together.

After deployment, open the homepage, quickstart and API reference. Inspect the page source for:

- A descriptive title and description that match the page's content.
- One canonical link pointing to that page under the published base URL.
- No `noindex` instruction on a normal documentation page. Old redirect aliases deliberately
  retain `noindex` and point to their replacement pages.
- A successful response for [sitemap.xml](https://alexandrust88.github.io/openbao-config-operator/sitemap.xml),
  containing the published documentation URLs.

There is no need to add `robots.txt` under the project path: Google reads it at the **host root**,
`https://alexandrust88.github.io/robots.txt`. A missing file returning 404 is not a crawl restriction.
If the account's root site later supplies a robots file, check that it does not disallow this
project. See [Google's robots.txt rules](https://developers.google.com/crawling/docs/robots-txt/robots-txt-spec).

## Verify ownership in Search Console

1. Open [Google Search Console](https://search.google.com/search-console) with the account that
   should own this documentation property.
2. Add a **URL-prefix property** with exactly
   `https://alexandrust88.github.io/openbao-config-operator/`. This verifies the project website;
   it does not require control of the `github.io` domain or the GitHub repository's HTML.
3. Choose **HTML file upload** if Search Console offers it. Download the actual verification
   file from Google and put it, unchanged, in `docs/`, preserving its exact filename. MkDocs copies
   static HTML files to the site root, so the file is served beneath the project base URL.
4. Build and deploy through the Pages workflow. Open the exact verification URL Google specifies
   and confirm it returns the file, then select **Verify** in Search Console.
5. Keep the verification file deployed. Removing it can cause ownership verification to expire.

Use the verification method offered for your property; do not invent a token or reuse somebody
else's. Google also supports HTML tag verification if file upload is unsuitable. Follow its
[ownership verification instructions](https://support.google.com/webmasters/answer/9008080).

## Submit the sitemap and inspect important pages

In the verified property's **Sitemaps** report, submit:

```text
https://alexandrust88.github.io/openbao-config-operator/sitemap.xml
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

Write specific titles and descriptions for important pages. The theme appends the project name,
so repeating it in every frontmatter title makes titles redundant. Google may use page text
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
