// 入口：把折扣、运费和共享类型拆到三个文件后，仍然只生成一个原生程序。
import { discount } from "./discount.ts";
import { shipping } from "./shipping.ts";
import type { Money } from "./types.ts";

const price: Money = 120;
const reduced = discount(price, 25);
const freight = shipping(2);

console.log(reduced);
console.log(freight);
console.log(reduced + freight);
