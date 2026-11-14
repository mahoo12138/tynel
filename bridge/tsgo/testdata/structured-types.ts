// 结构化类型摘要的夹具。它不参与能力检查，只用来确认适配层能把对象、数组、
// 元组、函数和联合类型摊平成 Tynel 自有事实。
type Point = { x: number; y: number; label?: string };
type Names = string[];
type Pair = [number, boolean];
type Handler = (value: number, label?: string) => boolean;
type Mixed = number | string;

function make(x: number, y: number): Point {
  return { x, y };
}

function consume(point: Point): number {
  return point.x;
}

function pick(flag: boolean): Mixed {
  return flag ? 1 : "x";
}
