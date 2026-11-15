interface Point {
  x: number;
  y: number;
}

function sum(p: Point): number {
  return p.x + p.y;
}

const wider = { x: 1, y: 2, z: 3 };
console.log(sum(wider));
