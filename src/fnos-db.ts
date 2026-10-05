import { Database } from "bun:sqlite";
import type { FnPerson } from "./types.ts";

const REQUIRED_COLUMNS = ["guid", "trim_id", "tmdb_id", "imdb_id", "name", "original_name"];

/** 从 FnOS Media 数据库只读枚举本地 person 记录，不通过数据库写回任何内容。 */
export function loadLocalPeople(databasePath: string): FnPerson[] {
  const database = new Database(databasePath, { readonly: true });
  try {
    const columns = new Set(
      (database.query("PRAGMA table_info(person)").all() as Array<{ name: string }>).map((column) => column.name),
    );
    const missing = REQUIRED_COLUMNS.filter((column) => !columns.has(column));
    if (missing.length) {
      throw new Error(`FnOS person 表缺少字段：${missing.join(", ")}；数据库结构可能已随 FnOS 更新`);
    }
    const rows = database.query(`
      SELECT guid, trim_id, tmdb_id, imdb_id, name, original_name, biography, profile_path
      FROM person
      WHERE substr(trim_id, 1, 13) = 'LOCAL_PERSON_'
      ORDER BY name COLLATE NOCASE
    `).all() as unknown as FnPerson[];
    return rows.filter((person) =>
      /^LOCAL_PERSON_/i.test(person.trim_id || "") &&
      !person.tmdb_id &&
      !person.imdb_id,
    );
  } finally {
    database.close();
  }
}
