import { createHash } from "node:crypto";
import { readdir, readFile, mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { FnOSClient } from "./fnos.ts";
import { fetchPortrait } from "./image.ts";
import { parseActors } from "./nfo.ts";
import { scrapeGfriends } from "./providers/gfriends.ts";
import { scrapeMinnano } from "./providers/minnano.ts";
import { scrapeWikipedia } from "./providers/wikipedia.ts";
import type { ActorProfile, FnPerson } from "./types.ts";
import { normalizeName, unique } from "./util.ts";

interface Options { root: string; cache: string; actor?: string; limit: number; apply: boolean; overwrite: boolean; refresh: boolean; }
const help = `FnOS Actor Scraper — 补全飞牛影视中的本地演员档案

用法：
  bun src/index.ts [--actor 演员名 | --root NFO目录] [--apply] [--overwrite] [--refresh]

默认只预览，不写入飞牛资料。实际更新必须显式传 --apply。
批量模式仅读取媒体库 NFO 收集演员名称，不修改 NFO/媒体文件。

参数：
  --actor NAME     只处理指定演员（不需要挂载媒体目录）
  --root DIR       NFO 扫描目录（默认 MEDIA_ROOT 或 /media）
  --cache DIR      缓存目录（默认 CACHE_DIR 或 /config）
  --limit N        最多处理 N 个演员，0 表示不限制
  --apply          将缺少的头像/简介写入飞牛影视本地演员档案
  --overwrite      覆盖已有头像/简介（仍跳过飞牛官方资料及锁定字段）
  --refresh        忽略演员资料缓存并重新查询来源
  --help           显示帮助

环境变量：FNOS_URL、FNOS_USERNAME、FNOS_PASSWORD（或 FNOS_TOKEN）。`;

function optionsFromArgs(args: string[]): Options {
  const options: Options = { root: process.env.MEDIA_ROOT || "/media", cache: process.env.CACHE_DIR || "/config", limit: 0, apply: false, overwrite: false, refresh: false };
  for (let i = 0; i < args.length; i++) {
    const arg = args[i]!;
    if (arg === "--help" || arg === "-h") { console.log(help); process.exit(0); }
    if (arg === "--apply") options.apply = true;
    else if (arg === "--overwrite") options.overwrite = true;
    else if (arg === "--refresh") options.refresh = true;
    else if (["--actor", "--root", "--cache", "--limit"].includes(arg)) {
      const value = args[++i];
      if (!value || value.startsWith("--")) throw new Error(`${arg} 需要参数值`);
      if (arg === "--actor") options.actor = value.trim();
      else if (arg === "--root") options.root = value;
      else if (arg === "--cache") options.cache = value;
      else { options.limit = Number(value); if (!Number.isInteger(options.limit) || options.limit < 0) throw new Error("--limit 必须是非负整数"); }
    } else throw new Error(`未知参数：${arg}\n\n${help}`);
  }
  if (options.overwrite && !options.apply) throw new Error("--overwrite 需要同时指定 --apply");
  if (options.actor && !options.actor.trim()) throw new Error("--actor 不能为空");
  return options;
}

async function scanActors(root: string): Promise<Map<string, { name: string; count: number }>> {
  const actors = new Map<string, { name: string; count: number }>();
  const visit = async (directory: string): Promise<void> => {
    let entries;
    try { entries = await readdir(directory, { withFileTypes: true }); }
    catch (error) { console.warn(`跳过无法读取的目录 ${directory}: ${String(error)}`); return; }
    for (const entry of entries) {
      const path = join(directory, entry.name);
      if (entry.isSymbolicLink()) continue;
      if (entry.isDirectory()) await visit(path);
      else if (entry.isFile() && entry.name.toLowerCase().endsWith(".nfo")) {
        try {
          const { actors: found } = parseActors(await readFile(path, "utf8"));
          for (const item of found) {
            const key = normalizeName(item.name);
            const prior = actors.get(key);
            actors.set(key, { name: prior?.name || item.name, count: (prior?.count || 0) + 1 });
          }
        } catch (error) { console.warn(`跳过无法解析的 NFO ${path}: ${String(error)}`); }
      }
    }
  };
  await visit(resolve(root));
  return actors;
}

function isLocalPerson(person: FnPerson): boolean {
  return person.is_official !== true && /^LOCAL_PERSON_/i.test(person.trim_id || "") && !person.tmdb_id && !person.imdb_id;
}

function exactNameMatch(person: FnPerson, name: string): boolean {
  return [person.name, person.original_name].some((candidate) => candidate && normalizeName(candidate) === normalizeName(name));
}

async function scrapedProfile(name: string, cacheDir: string, refresh: boolean): Promise<ActorProfile | undefined> {
  const cacheKey = createHash("sha256").update(name.normalize("NFKC")).digest("hex");
  const file = join(cacheDir, "actors", `${cacheKey}.json`);
  if (!refresh) {
    try {
      const cached = JSON.parse(await readFile(file, "utf8")) as ActorProfile | null;
      if (cached) return cached;
    } catch { /* no valid cache */ }
  }
  const outcomes = await Promise.allSettled([
    scrapeGfriends(name, cacheDir, refresh),
    scrapeMinnano(name),
    scrapeWikipedia(name),
  ]);
  const profiles = outcomes.flatMap((result, index) => {
    if (result.status === "fulfilled" && result.value) return [result.value];
    if (result.status === "rejected") console.warn(`${name} 的来源 ${["Gfriends", "Minnano-AV", "Wikipedia"][index]} 查询失败：${String(result.reason)}`);
    return [];
  });
  const merged: ActorProfile | undefined = profiles.length ? {
    name,
    aliases: unique(profiles.flatMap((profile) => profile.aliases)),
    imageUrl: profiles.find((profile) => profile.imageUrl)?.imageUrl,
    biography: profiles.find((profile) => profile.biography?.trim())?.biography?.trim(),
    sourceUrls: unique(profiles.flatMap((profile) => profile.sourceUrls)),
    sourceNames: unique(profiles.flatMap((profile) => profile.sourceNames)),
  } : undefined;
  await mkdir(join(cacheDir, "actors"), { recursive: true });
  await writeFile(file, JSON.stringify(merged ?? null), "utf8");
  return merged;
}

async function main(): Promise<void> {
  const options = optionsFromArgs(Bun.argv.slice(2));
  if (!options.actor && !options.root) throw new Error("请提供 --actor 或 --root");
  const names = options.actor
    ? new Map([[normalizeName(options.actor), { name: options.actor, count: 1 }]])
    : await scanActors(options.root);
  const selected = [...names.values()].slice(0, options.limit || undefined);
  console.log(`收集到 ${names.size} 个演员名称，本次处理 ${selected.length} 个。模式：${options.apply ? "写入飞牛" : "预览"}`);
  if (!selected.length) return;

  const baseUrl = process.env.FNOS_URL;
  if (!baseUrl) throw new Error("请设置 FNOS_URL，例如 https://192.168.1.10:5667");
  const url = new URL(baseUrl);
  if (url.protocol !== "https:" && !["localhost", "127.0.0.1"].includes(url.hostname)) throw new Error("FNOS_URL 必须使用 HTTPS（本机 localhost 可使用 HTTP）");
  const client = new FnOSClient(baseUrl, process.env.FNOS_USERNAME || "", process.env.FNOS_PASSWORD || "", process.env.FNOS_TOKEN);
  await client.login();

  for (const item of selected) {
    const name = item.name;
    console.log(`\n[${name}] NFO 出现 ${item.count} 次`);
    const matches = (await client.searchPeople(name)).filter((person) => exactNameMatch(person, name));
    if (!matches.length) { console.log("  跳过：飞牛中没有同名演员档案（本程序不会新建档案）"); continue; }
    if (matches.length > 1) { console.log(`  跳过：找到 ${matches.length} 个同名档案，无法安全判断目标`); continue; }
    const detail = await client.getEditDetail(matches[0]!.guid);
    if (!isLocalPerson(detail)) { console.log("  跳过：该档案不是可安全修改的本地演员资料（官方/在线资料受保护）"); continue; }

    const profile = await scrapedProfile(name, options.cache, options.refresh);
    if (!profile || (!profile.imageUrl && !profile.biography)) { console.log("  未找到可用头像或简介"); continue; }
    const canBio = Boolean(profile.biography && !detail.biography_locked && (options.overwrite || !detail.biography?.trim()));
    const canImage = Boolean(profile.imageUrl && !detail.profile_path_locked && (options.overwrite || !detail.profile_path?.trim()));
    if (!canBio && !canImage) { console.log("  跳过：头像和简介都已存在或字段已锁定"); continue; }

    const image = canImage ? await fetchPortrait(profile.imageUrl!) : undefined;
    console.log(`  来源：${profile.sourceNames.join(", ") || "未知"}`);
    console.log(`  将更新：${[canImage && "头像", canBio && "简介"].filter(Boolean).join("、")}`);
    if (!options.apply) continue;
    const profilePath = image ? await client.uploadProfile(image) : undefined;
    await client.saveProfile(detail, canBio ? profile.biography : undefined, profilePath);
    console.log("  已写入飞牛演员档案");
  }
}

main().catch((error) => { console.error(`错误：${error instanceof Error ? error.message : String(error)}`); process.exitCode = 1; });
