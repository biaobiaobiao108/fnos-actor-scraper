import { createHash } from "node:crypto";
import type { FnPerson } from "./types.ts";

type ApiResult<T> = { code?: number; msg?: string; message?: string; data?: T };

export class FnOSClient {
  private token?: string;
  private readonly base: string;

  constructor(baseUrl: string, private readonly username: string, private readonly password: string, token?: string) {
    this.base = `${baseUrl.replace(/\/+$/, "")}/v/api/v1`;
    this.token = token;
  }

  private async request<T>(path: string, init: RequestInit = {}): Promise<T> {
    const headers = new Headers(init.headers);
    headers.set("accept", "application/json");
    if (this.token) headers.set("authorization", this.token);
    if (init.body && !(init.body instanceof FormData)) headers.set("content-type", "application/json");
    const response = await fetch(`${this.base}${path}`, { ...init, headers, signal: AbortSignal.timeout(20_000) });
    const body = await response.json() as ApiResult<T>;
    if (!response.ok || (typeof body.code === "number" && body.code !== 0)) {
      throw new Error(`FnOS API ${path}: ${body.msg || body.message || response.statusText} (${response.status}/${body.code ?? "?"})`);
    }
    return (body.data ?? body) as T;
  }

  async login(): Promise<void> {
    if (this.token) return;
    if (!this.username || !this.password) throw new Error("请设置 FNOS_USERNAME 和 FNOS_PASSWORD，或提供 FNOS_TOKEN");
    const password = createHash("sha256").update(this.password).digest("hex");
    const data = await this.request<{ token?: string }>("/user/loginByPassword?channel=v2", {
      method: "POST",
      body: JSON.stringify({ username: this.username, password }),
    });
    if (!data.token) throw new Error("飞牛登录成功响应中没有 token；请检查 FnOS 版本或登录接口兼容性");
    this.token = data.token;
  }

  async searchPeople(keyword: string): Promise<FnPerson[]> {
    const data = await this.request<unknown>("/person/search", {
      method: "POST",
      body: JSON.stringify({ keyword, page: 1, page_size: 50 }),
    });
    if (Array.isArray(data)) return data as FnPerson[];
    if (data && typeof data === "object") {
      const value = data as Record<string, unknown>;
      for (const key of ["list", "items", "records", "persons", "data"]) {
        if (Array.isArray(value[key])) return value[key] as FnPerson[];
      }
    }
    return [];
  }

  async getEditDetail(guid: string): Promise<FnPerson> {
    return this.request<FnPerson>("/person/getEditDetail", { method: "POST", body: JSON.stringify({ guid }) });
  }

  async uploadProfile(image: Uint8Array): Promise<string> {
    const form = new FormData();
    const buffer = new ArrayBuffer(image.byteLength);
    new Uint8Array(buffer).set(image);
    form.set("file", new Blob([buffer], { type: "image/jpeg" }), "profile.jpg");
    const result = await this.request<{ hash_path?: string }>("/image/temp/upload", { method: "POST", body: form });
    if (!result.hash_path) throw new Error("飞牛图片上传响应中没有 hash_path");
    return result.hash_path;
  }

  async saveProfile(person: FnPerson, biography: string | undefined, profilePath: string | undefined): Promise<void> {
    await this.request("/person/saveEditDetail", {
      method: "POST",
      body: JSON.stringify({
        guid: person.guid,
        is_official: Boolean(person.is_official),
        name: person.name || person.original_name || "",
        name_locked: Boolean(person.name_locked),
        biography: biography ?? person.biography ?? "",
        biography_locked: Boolean(person.biography_locked),
        profile_path: profilePath ?? person.profile_path ?? "",
        profile_path_locked: Boolean(person.profile_path_locked),
      }),
    });
  }
}
