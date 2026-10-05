import type { ActorProfile } from "../types.ts";
import { fetchJson, nameSimilarity } from "../util.ts";

interface WikiPage {
  title?: string;
  extract?: string;
  thumbnail?: { source?: string };
}

export async function scrapeWikipedia(name: string): Promise<ActorProfile | undefined> {
  for (const language of ["zh", "ja"]) {
    try {
      const params = new URLSearchParams({
        action: "query",
        generator: "search",
        gsrsearch: name,
        gsrnamespace: "0",
        gsrlimit: "5",
        prop: "extracts|pageimages",
        exintro: "1",
        explaintext: "1",
        piprop: "thumbnail",
        pithumbsize: "640",
        format: "json",
        formatversion: "2",
        utf8: "1",
      });
      const endpoint = `https://${language}.wikipedia.org/w/api.php?${params.toString()}`;
      const response = await fetchJson<{ query?: { pages?: WikiPage[] } }>(endpoint);
      const summary = (response.query?.pages || [])
        .map((page) => ({ ...page, score: nameSimilarity(name, page.title || "") }))
        .filter((page) => page.score >= (Array.from(name).length >= 4 ? 0.7 : 1))
        .sort((a, b) => b.score - a.score)[0];
      if (!summary) continue;
      if (!summary.extract && !summary.thumbnail?.source) continue;
      return {
        name,
        aliases: summary.title && summary.title !== name ? [summary.title] : [],
        imageUrl: summary.thumbnail?.source,
        biography: summary.extract,
        sourceUrls: [`https://${language}.wikipedia.org/wiki/${encodeURIComponent(summary.title || name)}`],
        sourceNames: [`Wikipedia (${language})`],
      };
    } catch {
      // Try the other language edition when the exact page title is missing.
    }
  }
  return undefined;
}
