import type { Money } from "./types.ts";

// 满减前的折扣：按百分比减去一部分价格。
export function discount(price: Money, percent: number): Money {
  return price - price * percent / 100;
}
