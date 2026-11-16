function add(a: number, b?: number): number {
  if (b === undefined) {
    return a;
  }
  return a + b;
}

function pick(text: string, suffix?: string): string {
  return text + (suffix ?? "!");
}

function twice(a: number, b?: number, c?: number): number {
  return a + (b ?? 0) + (c ?? 0);
}

function show(value: string | undefined): string {
  return value ?? "empty";
}

function steps(n: number): number {
  let count: number = 0;
  while (add(0) < n && count < 2) {
    count = count + 1;
  }
  return count;
}

function both(flag: boolean): number {
  let total: number = 0;
  if (flag && add(1) > 0) {
    total = add(2);
  }
  return total;
}

console.log(add(3));
console.log(add(3, 4));
console.log(add(3, undefined));
console.log(pick("hi"));
console.log(pick("hi", "?"));
console.log(twice(1));
console.log(twice(1, 2));
console.log(twice(1, 2, 3));
console.log(show("x"));
console.log(show(undefined));
console.log(steps(1));
console.log(both(true));
console.log(both(false));
