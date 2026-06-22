import { mkdirSync } from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";

export class SqliteCli {
  constructor(readonly dbPath: string) {
    mkdirSync(path.dirname(dbPath), { recursive: true });
  }

  exec(sql: string): void {
    const result = spawnSync("sqlite3", [this.dbPath, sql], { encoding: "utf8" });
    if (result.status !== 0) throw new Error(result.stderr.trim() || "sqlite3 failed");
  }

  transaction(sql: string): void {
    const result = spawnSync("sqlite3", [this.dbPath], {
      encoding: "utf8",
      input: `.bail on\nBEGIN IMMEDIATE;\n${sql}\nCOMMIT;\n`,
    });
    if (result.status !== 0) throw new Error(result.stderr.trim() || "sqlite3 transaction failed");
  }

  query<T>(sql: string): T[] {
    const result = spawnSync("sqlite3", ["-json", this.dbPath, sql], { encoding: "utf8" });
    if (result.status !== 0) throw new Error(result.stderr.trim() || "sqlite3 query failed");
    const text = result.stdout.trim();
    return text ? (JSON.parse(text) as T[]) : [];
  }
}

export function sqlString(value: string): string {
  return `'${value.replaceAll("'", "''")}'`;
}
