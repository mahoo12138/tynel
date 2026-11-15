interface Point {
  x: number;
  y: number;
}

interface Segment {
  start: Point;
  end: Point;
}

type Pair = { x: number; y: number };

function sum(p: Point): number {
  return p.x + p.y;
}

function mark(n: number): number {
  console.log(n);
  return n;
}

const p: Point = { x: mark(1), y: mark(2) };
const q: Point = { x: 1, y: 2 };
const s: Segment = { start: { x: 10, y: 20 }, end: { x: 30, y: 40 } };
const empty = {};
const r: Pair = p;

console.log(sum(p));
console.log(p === p);
console.log(p === q);
console.log(s.start.x + s.end.y);
console.log(empty === empty);
console.log(r === p);

const chosen = p.x > 0 ? p : q;
console.log(chosen === p);
