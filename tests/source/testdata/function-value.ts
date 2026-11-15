type Unary = (x: number) => number;

function add(a: number, b: number): number {
  return a + b;
}

function sub(a: number, b: number): number {
  return a - b;
}

const inc: Unary = (x: number): number => x + 1;

function apply(value: number, f: Unary): number {
  return f(value);
}

function make(): Unary {
  return (x: number): number => x + 2;
}

function note(): void {
  console.log(99);
}

function demo(): void {
  const alias = add;
  console.log(alias(20, 22));

  const local: Unary = (x: number): number => x + 1;
  console.log(apply(5, local));

  const plus2 = make();
  console.log(plus2(40));

  const f: () => void = note;
  f();

  const choose = true ? add : sub;
  console.log(choose(1, 2));
}

console.log(inc(4));
demo();
