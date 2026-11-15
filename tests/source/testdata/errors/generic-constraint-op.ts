function bump<T extends number>(value: T): number {
  return value + 1;
}

function demo(): void {
  console.log(bump(1));
}

demo();
