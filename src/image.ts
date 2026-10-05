import sharp from "sharp";

const MAX_DOWNLOAD = 10 * 1024 * 1024;
const MAX_UPLOAD = 4 * 1024 * 1024;

function isPublicHost(host: string): boolean {
  const value = host.toLowerCase().replace(/^\[|\]$/g, "");
  if (value === "localhost" || value.endsWith(".localhost") || value.endsWith(".local")) return false;
  if (/^(\d{1,3}\.){3}\d{1,3}$/.test(value)) {
    const [a, b] = value.split(".").map(Number);
    return a !== 10 && a !== 127 && a !== 0 && !(a === 169 && b === 254) && !(a === 172 && b >= 16 && b <= 31) && !(a === 192 && b === 168);
  }
  return !value.includes(":"); // IPv6 literals are intentionally rejected.
}

export async function fetchPortrait(urlText: string): Promise<Uint8Array> {
  const url = new URL(urlText);
  if (url.protocol !== "https:" || !isPublicHost(url.hostname)) throw new Error("头像地址必须是公网 HTTPS 地址");
  const response = await fetch(url, { redirect: "error", signal: AbortSignal.timeout(20_000), headers: { "user-agent": "FnOS-Actor-Scraper/0.2", accept: "image/*" } });
  if (!response.ok) throw new Error(`头像下载失败：HTTP ${response.status}`);
  const declared = Number(response.headers.get("content-length") || 0);
  if (declared > MAX_DOWNLOAD) throw new Error("头像原图超过 10 MiB 限制");
  const source = new Uint8Array(await response.arrayBuffer());
  if (source.byteLength > MAX_DOWNLOAD) throw new Error("头像原图超过 10 MiB 限制");
  const metadata = await sharp(source, { limitInputPixels: 40_000_000 }).metadata();
  if (!metadata.width || !metadata.height) throw new Error("无法识别头像图片");
  const output = await sharp(source, { limitInputPixels: 40_000_000 })
    .rotate()
    .resize(640, 960, { fit: "cover", position: "centre" })
    .jpeg({ quality: 88, mozjpeg: true })
    .toBuffer();
  if (output.byteLength > MAX_UPLOAD) throw new Error("处理后的头像仍超过飞牛 4 MiB 上传限制");
  return new Uint8Array(output);
}
