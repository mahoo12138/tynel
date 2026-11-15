function identity<T>(value: T): T {
  return value;
}

function demo(): void {
  const f = identity;
  console.log(f(1));
}

demo();
