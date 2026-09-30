// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// The content loader of the documentation site (ADR-0033): it reads the
// Markdown of docs/ in place — the files readers see on GitHub — and the
// few pages only the site has (src/content/docs/). A page's title is its
// first heading. Relative links are rewritten: to the page's route when
// they name a page of the site, to GitHub when they name another file of
// the repository. A link to nothing fails the build.
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { join, posix, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
import type { Loader } from "astro/loaders";

/** Where links to files outside the site point. */
const blob = "https://github.com/nightCode42/plux3/blob/main/";

/** Where a page is edited. */
const edit = "https://github.com/nightCode42/plux3/edit/main/";

/** Files of docs/ that are not published: the maintainers' work log. */
const unpublished = new Set(["WORKLOG.md", "worklog-archive.md"]);

/** A page: where it comes from, its route, and whether it is a directory's index. */
interface Page {
  file: string;
  id: string;
  index: boolean;
}

/** Every Markdown file under dir, relative to it, with forward slashes. */
function markdownFiles(dir: string): string[] {
  const out: string[] = [];
  const walk = (d: string) => {
    for (const name of readdirSync(d).sort()) {
      const path = join(d, name);
      if (statSync(path).isDirectory()) walk(path);
      else if (name.endsWith(".md")) out.push(relative(dir, path).split(sep).join("/"));
    }
  };
  walk(dir);
  return out;
}

/** The route of a file of docs/: README.md names its directory. */
export function routeOf(path: string): string {
  const id = path
    .replace(/\.md$/, "")
    .replace(/(^|\/)README$/, "")
    .toLowerCase();
  return id === "" ? "index" : id;
}

/** The first level-one heading and the text without it. */
export function splitTitle(text: string, fallback: string): { title: string; body: string } {
  const match = /^# (.+)\n+/m.exec(text);
  if (!match) return { title: fallback, body: text };
  return {
    title: match[1].trim(),
    body: text.slice(0, match.index) + text.slice(match.index + match[0].length),
  };
}

/**
 * Rewrites the relative links and images of a page at repo/file: pages
 * to their routes under base, other files to GitHub. Returns the text and
 * the links that name nothing.
 */
export function rewriteLinks(
  text: string,
  file: string,
  repo: string,
  routes: Map<string, string>,
  base: string,
): { text: string; broken: string[] } {
  const broken: string[] = [];
  const target = (href: string): string => {
    if (/^([a-z][a-z0-9+.-]*:|#|\/)/i.test(href)) return href;
    const [path, anchor] = href.split("#", 2);
    const abs = posix.normalize(posix.join(posix.dirname(file), decodeURI(path)));
    const suffix = anchor === undefined ? "" : `#${anchor}`;
    const route = routes.get(abs);
    if (route !== undefined) {
      return `${base}/${route === "index" ? "" : `${route}/`}${suffix}`;
    }
    if (abs.startsWith("..") || !existsSync(join(repo, abs))) {
      broken.push(href);
      return href;
    }
    return `${blob}${abs}${suffix}`;
  };
  // Code must not be rewritten: split out fenced blocks and inline code.
  const parts = text.split(/(```[\s\S]*?```|`[^`\n]*`)/);
  for (let i = 0; i < parts.length; i += 2) {
    parts[i] = parts[i]
      .replace(/(!?\[[^\]]*\]\()([^)\s]+)(\s+"[^"]*")?\)/g, (_, open, href, title) => `${open}${target(href)}${title ?? ""})`)
      .replace(/^(\[[^\]]+\]:\s+)(\S+)/gm, (_, open, href) => `${open}${target(href)}`);
  }
  return { text: parts.join(""), broken };
}

/** The loader of the site's docs collection. */
export function pluxDocs(): Loader {
  return {
    name: "plux-docs-loader",
    async load({ config, store, parseData, renderMarkdown, generateDigest, logger }) {
      const site = fileURLToPath(config.root);
      const repo = resolve(site, "..");
      const pages: Page[] = [];
      for (const path of markdownFiles(join(repo, "docs"))) {
        if (!unpublished.has(path)) {
          pages.push({ file: `docs/${path}`, id: routeOf(path), index: /(^|\/)README\.md$/.test(path) });
        }
      }
      const own = join(site, "src", "content", "docs");
      for (const path of markdownFiles(own)) {
        pages.push({ file: relative(repo, join(own, path)).split(sep).join("/"), id: routeOf(path), index: false });
      }
      const routes = new Map(pages.map((p) => [p.file, p.id]));
      const base = config.base.replace(/\/$/, "");
      const broken: string[] = [];
      store.clear();
      for (const page of pages) {
        const raw = readFileSync(join(repo, page.file), "utf8");
        const front = /^---\n([\s\S]*?)\n---\n/.exec(raw);
        const data: Record<string, unknown> = {};
        let text = raw;
        if (front) {
          // The site's own pages carry their title in front matter.
          for (const line of front[1].split("\n")) {
            const m = /^(\w+):\s*(.*)$/.exec(line);
            if (m) data[m[1]] = m[2].replace(/^"(.*)"$/, "$1");
          }
          text = raw.slice(front[0].length);
        } else {
          const split = splitTitle(raw, page.id);
          data.title = split.title;
          text = split.body;
        }
        const rewritten = rewriteLinks(text, page.file, repo, routes, base);
        for (const href of rewritten.broken) broken.push(`${page.file}: ${href}`);
        data.editUrl ??= `${edit}${page.file}`;
        const parsed = await parseData({ id: page.id, data, filePath: join(repo, page.file) });
        store.set({
          id: page.id,
          data: parsed,
          body: rewritten.text,
          // Starlight groups the sidebar by the path under its content
          // directory; the real file is in editUrl.
          filePath: `src/content/docs/${page.index ? `${page.id}/index` : page.id}.md`,
          digest: generateDigest(raw),
          rendered: await renderMarkdown(rewritten.text),
        });
      }
      if (broken.length > 0) {
        throw new Error(`broken links in the documentation:\n  ${broken.join("\n  ")}`);
      }
      logger.info(`${pages.length} pages from docs/ and src/content/docs/`);
    },
  };
}
