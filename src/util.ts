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

let upstreamQueue: Promise<void> = Promise.resolve();

function upstreamDelayMs(): number {
  const configured = Number(process.env.UPSTREAM_DELAY_MS || 2_000);
  if (!Number.isFinite(configured)) return 2_000;
  return Math.max(500, Math.min(60_000, Math.floor(configured)));
}

function retryAfterMs(response: Response, attempt: number): number {
  const value = response.headers.get("retry-after");
  if (value) {
    const seconds = Number(value);
    const dateDelay = Date.parse(value) - Date.now();
    const delay = Number.isFinite(seconds) ? seconds * 1_000 : dateDelay;
    if (Number.isFinite(delay) && delay > 0) return Math.min(delay, 60_000);
  }
  return Math.min(1_000 * 2 ** attempt, 15_000);
}

async function readResponseBody(response: Response, maxBytes: number): Promise<ArrayBuffer | null> {
  if (!response.body) return null;
  const declared = Number(response.headers.get("content-length") || 0);
  if (declared > maxBytes) {
    await response.body.cancel();
    throw new Error(`上游响应超过 ${maxBytes} 字节限制`);
  }
  const reader = response.body.getReader();
  // Keep the chunks only until the final allocation; never accept an unbounded body.
  // If Content-Length is known, allocate once and fill it directly to avoid retaining
  // both a chunk list and a second full-sized copy for large JSON/image responses.
  if (declared > 0 && !response.headers.has("content-encoding")) {
    const buffer = new ArrayBuffer(declared);
    const output = new Uint8Array(buffer);
    let offset = 0;
    try {
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        if (offset + value.byteLength > declared || offset + value.byteLength > maxBytes) {
          await reader.cancel();
          throw new Error(`上游响应超过 ${maxBytes} 字节限制`);
        }
        output.set(value, offset);
        offset += value.byteLength;
      }
    } catch (error) {
      if (offset < declared) await reader.cancel().catch(() => undefined);
      throw error;
    }
    return offset === declared ? buffer : buffer.slice(0, offset);
  }
  const chunks: Uint8Array[] = [];
  let total = 0;
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    total += value.byteLength;
    if (total > maxBytes) {
      await reader.cancel();
      throw new Error(`上游响应超过 ${maxBytes} 字节限制`);
    }
    chunks.push(value);
  }
  const buffer = new ArrayBuffer(total);
  const output = new Uint8Array(buffer);
  let offset = 0;
  for (const chunk of chunks) { output.set(chunk, offset); offset += chunk.byteLength; }
  return buffer;
}

/** Serialize upstream HTTP calls, spacing each request and backing off on throttling/server errors. */
export function fetchUpstream(url: string | URL, init: RequestInit = {}, maxBodyBytes = 16 * 1024 * 1024): Promise<Response> {
  const run = async (): Promise<Response> => {
    const delay = upstreamDelayMs();
    for (let attempt = 0; attempt < 3; attempt++) {
      await Bun.sleep(delay);
      let response: Response;
      try {
        response = await fetch(url, init);
      } catch (error) {
        if (attempt === 2) throw error;
        await Bun.sleep(Math.min(1_000 * 2 ** attempt, 15_000));
        continue;
      }
      if (![429, 500, 502, 503, 504].includes(response.status) || attempt === 2) {
        const body = [204, 205, 304].includes(response.status) ? null : await readResponseBody(response, maxBodyBytes);
        return new Response(body, { status: response.status, statusText: response.statusText, headers: response.headers });
      }
      const waitMs = retryAfterMs(response, attempt);
      await response.body?.cancel();
      console.warn(`上游返回 HTTP ${response.status}，等待 ${Math.ceil(waitMs / 1_000)} 秒后重试`);
      await Bun.sleep(waitMs);
    }
    throw new Error("上游请求重试次数已耗尽");
  };

  const result = upstreamQueue.then(run, run);
  upstreamQueue = result.then(() => undefined, () => undefined);
  return result;
}

export async function fetchText(url: string): Promise<string> {
  const response = await fetchUpstream(url, {
    headers: {
      "user-agent": "FnOS-Actor-Scraper/0.2 (+local media metadata utility)",
      accept: "text/html,application/json;q=0.9,*/*;q=0.8",
    },
    signal: AbortSignal.timeout(10_000),
  });
  if (!response.ok) throw new Error(`${response.status} ${response.statusText}: ${url}`);
  return response.text();
}

export async function fetchJson<T>(url: string, timeoutMs = 10_000): Promise<T> {
  const response = await fetchUpstream(url, {
    headers: {
      "user-agent": "FnOS-Actor-Scraper/0.2 (+local media metadata utility)",
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
