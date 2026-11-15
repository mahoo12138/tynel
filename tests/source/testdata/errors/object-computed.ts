interface Point {
  x: number;
}

const point: Point = { ["x"]: 1 };
console.log(point.x);
