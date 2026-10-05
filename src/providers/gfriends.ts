import { mkdir, readFile, stat, writeFile } from "node:fs/promises";
import { join } from "node:path";
import type { ActorProfile } from "../types.ts";
import { fetchJson } from "../util.ts";

type FileTree = {
  Content?: Record<string, unknown>;
};

const TREE_URL = "https://cdn.jsdelivr.net/gh/gfriends/gfriends@master/Filetree.json";

let treeInFlight: { cacheDir: string; promise: Promise<FileTree> } | undefined;

async function loadTree(cacheDir: string, refresh: boolean): Promise<FileTree> {
  const filePath = join(cacheDir, "gfriends-filetree.json");
  if (!refresh) {
    try {
      const file = Bun.file(filePath);
      if (await file.exists()) {
        const cachedAt = (await stat(filePath)).mtimeMs;
        if (Date.now() - cachedAt < 24 * 60 * 60 * 1000) {
          return JSON.parse(await readFile(filePath, "utf8")) as FileTree;
        }
      }
    } catch {
      // A stale or invalid cache is replaced by the next download.
    }
  }

  const tree = await fetchJson<FileTree>(TREE_URL);
  await mkdir(cacheDir, { recursive: true });
  await writeFile(filePath, JSON.stringify(tree), "utf8");
  return tree;
}

function getTree(cacheDir: string, refresh: boolean): Promise<FileTree> {
  // Filetree.json is shared by every actor. Share both parsing and downloads
  // within the process instead of loading the full tree once per worker.
  if (treeInFlight?.cacheDir === cacheDir) return treeInFlight.promise;
  const promise = loadTree(cacheDir, refresh).catch((error) => {
    if (treeInFlight?.promise === promise) treeInFlight = undefined;
    throw error;
  });
  treeInFlight = { cacheDir, promise };
  return promise;
}

function imageCandidates(tree: FileTree, name: string): string[] {
  const wanted = name.normalize("NFKC").toLocaleLowerCase().replace(/\.(jpe?g|png)$/i, "");
  const matches: string[] = [];
  const walk = (node: unknown, path: string[]) => {
    if (!node || typeof node !== "object") return;
    for (const [key, value] of Object.entries(node)) {
      if (key === "Information") continue;
      if (typeof value === "string") {
        const alias = key.replace(/\.(jpe?g|png)$/i, "").normalize("NFKC").toLocaleLowerCase();
        if (alias === wanted || alias.replace(/[\s\p{P}\p{S}]+/gu, "") === wanted.replace(/[\s\p{P}\p{S}]+/gu, "")) {
          matches.push([...path, value].join("/"));
        }
      } else {
        walk(value, [...path, key]);
      }
    }
  };
  walk(tree.Content, ["Content"]);
  return [...new Set(matches)];
}

function rawUrl(path: string): string {
  const [pathname, query] = path.split("?", 2);
  const encodedPath = pathname
    .split("/")
    .map((part) => encodeURIComponent(part))
    .join("/");
  return `https://raw.githubusercontent.com/gfriends/gfriends/master/${encodedPath}${query ? `?${query}` : ""}`;
}

export async function scrapeGfriends(name: string, cacheDir: string, refresh: boolean): Promise<ActorProfile | undefined> {
  const tree = await getTree(cacheDir, refresh);
  const imagePath = imageCandidates(tree, name)[0];
  if (!imagePath) return undefined;
  return {
    name,
    aliases: [],
    imageUrl: rawUrl(imagePath),
    sourceUrls: [TREE_URL, rawUrl(imagePath)],
    sourceNames: ["Gfriends"],
  };
}
