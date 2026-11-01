import type { Money } from "./types.ts";

// 运费：每件 5 元。
export function shipping(items: number): Money {
  return items * 5;
}
