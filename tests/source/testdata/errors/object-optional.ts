interface Option {
  x: number;
  y?: number;
}

const value: Option = { x: 1 };
console.log(value.x);
