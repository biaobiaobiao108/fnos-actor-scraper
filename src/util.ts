export function normalizeName(input: string): string {
  return input
    .normalize("NFKC")
    .toLocaleLowerCase()
    .replace(/[\s\p{P}\p{S}]+/gu, "");
}

export function nameSimilarity(left: string, right: string): number {
  const a = normalizeName(left);
  const b = normalizeName(right);
  if (!a || !b) return 0;
  if (a === b || a.includes(b) || b.includes(a)) return 1;
  const rightCharacters = new Set([...b]);
  const shared = [...a].filter((character) => rightCharacters.has(character)).length;
  return shared / Math.max([...a].length, rightCharacters.size);
}

export function unique(values: string[]): string[] {
  return [...new Set(values.map((value) => value.trim()).filter(Boolean))];
}

export async function fetchText(url: string): Promise<string> {
  const response = await fetch(url, {
    headers: {
      "user-agent": "FnOS-Actor-Scraper/0.1 (+local media metadata utility)",
      accept: "text/html,application/json;q=0.9,*/*;q=0.8",
    },
    signal: AbortSignal.timeout(10_000),
  });
  if (!response.ok) throw new Error(`${response.status} ${response.statusText}: ${url}`);
  return response.text();
}

export async function fetchJson<T>(url: string, timeoutMs = 10_000): Promise<T> {
  const response = await fetch(url, {
    headers: {
      "user-agent": "FnOS-Actor-Scraper/0.1 (+local media metadata utility)",
      accept: "application/json",
    },
    signal: AbortSignal.timeout(timeoutMs),
  });
  if (!response.ok) throw new Error(`${response.status} ${response.statusText}: ${url}`);
  return response.json() as Promise<T>;
}

export function cleanText(text: string | undefined): string | undefined {
  const cleaned = text?.replace(/\s+/g, " ").trim();
  return cleaned || undefined;
}
