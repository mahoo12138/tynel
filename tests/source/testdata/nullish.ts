function mark(n: number): number {
  console.log(n);
  return n;
}

function pick(value: number | null, fallback: number): number {
  return value ?? fallback;
}

function label(value: string | undefined): string {
  return value ?? "none";
}

function first(value: number | null): number {
  return value ?? mark(9);
}

function demo(): void {
  const a: number | null = 5;
  const b: number | null = null;
  const c: string | undefined = undefined;

  console.log(pick(a, 7));
  console.log(pick(b, 7));
  console.log(label("hi"));
  console.log(label(c));
  console.log(first(3));
  console.log(first(null));

  const d = a ?? mark(10);
  console.log(d);

  const e = b ?? mark(11) ?? 0;
  console.log(e);

  console.log(a ?? (b ?? mark(12)));

  let mutable: number | undefined = undefined;
  console.log(mutable ?? 4);
  mutable = 6;
  console.log(mutable ?? 4);

  if (a !== null) {
    console.log(a ?? 0);
  }
}

demo();
