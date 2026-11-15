function identity<T>(value: T): T {
  return value;
}

function first<T>(items: T[]): T {
  return items[0];
}

function pair<T>(a: T, b: T): T {
  return b;
}

function wrap<T>(value: T): T[] {
  return [value];
}

function apply<T>(f: (x: T) => T, value: T): T {
  return f(value);
}

function get<T>(box: { value: T }): T {
  return box.value;
}

function down<T>(value: T, n: number): T {
  if (n <= 0) {
    return value;
  }
  return down(value, n - 1);
}

function demo(): void {
  console.log(identity(42));
  console.log(identity("hi"));
  console.log(first([7, 8]));
  console.log(pair(3, 4));

  const xs = wrap(5);
  console.log(xs[0]);

  const inc = (x: number): number => x + 1;
  console.log(apply(inc, 4));

  const box = { value: pair(1, 2) };
  console.log(get(box));

  console.log(down(9, 3));
  console.log(identity(identity(5)));
}

demo();
