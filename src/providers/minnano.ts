import { load } from "cheerio";
import type { ActorProfile } from "../types.ts";
import { cleanText, fetchText, nameSimilarity, normalizeName, unique } from "../util.ts";

const BASE = "https://www.minnano-av.com";

function absoluteUrl(value: string | undefined): string | undefined {
  if (!value) return undefined;
  try {
    return new URL(value, BASE).toString();
  } catch {
    return undefined;
  }
}

export async function scrapeMinnano(name: string): Promise<ActorProfile | undefined> {
  const query = new URLSearchParams({ search_scope: "actress", search_word: name, search: " Go " });
  const searchUrl = `${BASE}/search_result.php?${query.toString()}`;
  const html = await fetchText(searchUrl);
  const $ = load(html);
  const candidates = new Map<string, { label: string; context: string; url: string }>();

  $("a[href]").each((_, element) => {
    const href = $(element).attr("href");
    const label = cleanText($(element).text());
    if (!href || !/actress\d+\.html(?:$|[?#])/i.test(href)) return;
    const url = absoluteUrl(href);
    const context = cleanText($(element).closest("li, tr, .actress, section, article").text()) || label || "";
    if (url) candidates.set(url, { label: label || context, context, url });
  });

  const candidate = [...candidates.values()]
    .map((item) => ({ ...item, score: Math.max(nameSimilarity(name, item.label), nameSimilarity(name, item.context)) }))
    .filter((item) => item.score >= (Array.from(normalizeName(name)).length >= 4 ? 0.7 : 1))
    .sort((a, b) => b.score - a.score)[0];
  if (!candidate) return undefined;

  const profileHtml = await fetchText(candidate.url);
  const page = load(profileHtml);
  const profileRoot = page(".actress-header .act-profile, .actress-header").first();
  const imageUrl =
    absoluteUrl(profileRoot.find("img").first().attr("data-src")) ||
    absoluteUrl(profileRoot.find("img").first().attr("src")) ||
    absoluteUrl(page('meta[property="og:image"]').attr("content"));
  const aliases = unique([
    candidate.label,
    ...profileRoot.find("a").toArray().map((el) => cleanText(page(el).text()) || ""),
  ]);

  const labeledFacts: string[] = [];
  page(".actress-header tr, .act-profile tr, .actress-profile tr").each((_, row) => {
    const cells = page(row).find("th,td").toArray().map((cell) => cleanText(page(cell).text()) || "");
    if (cells.length >= 2 && cells[0] && cells[1]) labeledFacts.push(`${cells[0]}：${cells[1]}`);
  });
  const biography = labeledFacts.length
    ? labeledFacts.join("；")
    : cleanText(profileRoot.find("p").map((_, p) => page(p).text()).get().join(" ")) ||
      cleanText(page('meta[name="description"]').attr("content"));

  return {
    name,
    aliases,
    imageUrl,
    biography,
    sourceUrls: [candidate.url],
    sourceNames: ["minnano-av"],
  };
}
