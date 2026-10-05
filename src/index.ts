import { copyFile, mkdir, readdir, readFile, stat, writeFile } from "node:fs/promises";
import { extname, join, resolve } from "node:path";
import type { ActorProfile, ScrapeOptions } from "./types.ts";
import { parseActors, serializeNfo, updateActor } from "./nfo.ts";
import { scrapeGfriends } from "./providers/gfriends.ts";
import { scrapeMinnano } from "./providers/minnano.ts";
import { scrapeWikipedia } from "./providers/wikipedia.ts";
import { normalizeName, unique } from "./util.ts";

const args = new Set(Bun.argv.slice(2));
const readOption = (name: string) => {
  const index = Bun.argv.indexOf(name);
  return index >= 0 ? Bun.argv[index + 1] : undefined;
};

const root = resolve(readOption("--root") || process.env.MEDIA_ROOT || "/media");
const cacheDir = resolve(readOption("--cache") || process.env.CACHE_DIR || "/config");
const applyChanges = args.has("--apply");
const refresh = args.has("--refresh");
const onlyActor = readOption("--actor");
const limit = Number(readOption("--limit") || "0");

async function walk(directory: string): Promise<string[]> {
  const result: string[] = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isSymbolicLink()) continue;
    if (entry.isDirectory()) result.push(...(await walk(path)));
    else if (entry.isFile() && extname(entry.name).toLocaleLowerCase() === ".nfo") result.push(path);
  }
  return result;
}

async function scrape(name: string, options: ScrapeOptions): Promise<ActorProfile | undefined> {
  const cachePath = join(options.cacheDir, "actors", `${new Bun.CryptoHasher("sha256").update(name).digest("hex")}.json`);
  if (!options.refresh && (await Bun.file(cachePath).exists())) {
    try {
      return JSON.parse(await readFile(cachePath, "utf8")) as ActorProfile;
    } catch {
      // Re-scrape if a previously cached record is malformed.
    }
  }

  const results = await Promise.allSettled([
    scrapeGfriends(name, options.cacheDir, options.refresh),
    scrapeMinnano(name),
    scrapeWikipedia(name),
  ]);
  const profiles: ActorProfile[] = [];
  const errors: string[] = [];
  for (const result of results) {
    if (result.status === "fulfilled") {
      if (result.value) profiles.push(result.value);
    } else {
      errors.push(result.reason instanceof Error ? result.reason.message : String(result.reason));
    }
  }

  if (!profiles.length) {
    if (errors.length) console.warn(`  来源不可用: ${errors.map((e) => e.split(":").slice(0, 2).join(":")).join(" | ")}`);
    return undefined;
  }

  const merged: ActorProfile = {
    name,
    aliases: unique(profiles.flatMap((profile) => profile.aliases)),
    imageUrl: profiles.find((profile) => profile.imageUrl)?.imageUrl,
    biography: profiles.find((profile) => profile.biography)?.biography,
    birthday: profiles.find((profile) => profile.birthday)?.birthday,
    sourceUrls: unique(profiles.flatMap((profile) => profile.sourceUrls)),
    sourceNames: unique(profiles.flatMap((profile) => profile.sourceNames)),
  };
  await mkdir(join(options.cacheDir, "actors"), { recursive: true });
  await writeFile(cachePath, JSON.stringify(merged, null, 2), "utf8");
  return merged;
}

async function main() {
  if (!(await stat(root).catch(() => undefined))) {
    throw new Error(`媒体目录不存在或不可访问: ${root}`);
  }
  const nfos = await walk(root);
  const actorOccurrences = new Map<string, number>();
  const pathsByActor = new Map<string, Set<string>>();
  const actorNameByKey = new Map<string, string>();
  const parsedFiles: Array<{ path: string; xml: string }> = [];

  for (const path of nfos) {
    const xml = await readFile(path, "utf8");
    try {
      const { actors } = parseActors(xml);
      parsedFiles.push({ path, xml });
      for (const actor of actors) {
        const key = normalizeName(actor.name);
        if (!key) continue;
        actorNameByKey.set(key, actorNameByKey.get(key) || actor.name);
        actorOccurrences.set(key, (actorOccurrences.get(key) || 0) + 1);
        const paths = pathsByActor.get(key) || new Set<string>();
        paths.add(path);
        pathsByActor.set(key, paths);
      }
    } catch (error) {
      console.warn(`跳过损坏 NFO ${path}: ${error instanceof Error ? error.message : error}`);
    }
  }

  const actors = [...actorNameByKey.entries()].filter(([, name]) => !onlyActor || normalizeName(name) === normalizeName(onlyActor));
  const selected = limit > 0 ? actors.slice(0, limit) : actors;
  console.log(`扫描到 ${nfos.length} 个 NFO、${actorOccurrences.size} 位演员；本次处理 ${selected.length} 位${applyChanges ? "（写入模式）" : "（预览模式）"}。`);

  const profiles = new Map<string, ActorProfile>();
  for (const [key, name] of selected) {
    console.log(`\n${name} — ${actorOccurrences.get(key)} 个影片 NFO`);
    const profile = await scrape(name, { cacheDir, refresh });
    if (!profile) {
      console.log("  没有找到可确认的在线演员资料");
      continue;
    }
    profiles.set(key, profile);
    console.log(`  来源: ${profile.sourceNames.join(", ")}`);
    console.log(`  头像: ${profile.imageUrl || "未找到"}`);
    console.log(`  简介/资料: ${profile.biography ? `${profile.biography.slice(0, 120)}${profile.biography.length > 120 ? "…" : ""}` : "未找到"}`);
    console.log(`  命中 NFO: ${pathsByActor.get(key)?.size || 0}`);
  }

  if (!applyChanges) {
    console.log("\n预览完成，没有修改 NFO。加 --apply 后才会写入；原 NFO 会先备份为 .nfo.actor-scraper.bak。");
    return;
  }

  let changedFiles = 0;
  for (const item of parsedFiles) {
    const parsed = parseActors(item.xml);
    let changed = false;
    for (const actor of parsed.actors) {
      const profile = profiles.get(normalizeName(actor.name));
      if (!profile) continue;
      changed = updateActor(parsed.document, actor, profile.imageUrl, profile.biography) || changed;
    }
    if (!changed) continue;

    const backup = `${item.path}.actor-scraper.bak`;
    if (!(await Bun.file(backup).exists())) await copyFile(item.path, backup);
    await writeFile(item.path, serializeNfo(parsed.document), "utf8");
    changedFiles++;
  }
  console.log(`\n已更新 ${changedFiles} 个 NFO；备份文件后缀为 .actor-scraper.bak。`);
  console.log("飞牛影视需要重新扫描媒体库才能读取 sidecar 更新。");
}

await main();
