function add(a: number, b: number): number {
  return a + b;
}

function demo(): void {
  const a = add;
  const b = add;
  console.log(a === b);
}

demo();
