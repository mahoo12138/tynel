function show(value: number | null): number {
  if (value !== null) {
    return value;
  }
  return 0;
}

function label(value: string | undefined): number {
  if (value !== undefined) {
    return value.length;
  }
  return -1;
}

function demo(): void {
  const a: number | null = 5;
  const b: number | null = null;
  console.log(show(a));
  console.log(show(b));

  if (a !== null) {
    console.log(a + 1);
  }
  if (b === null) {
    console.log(1);
  }

  let text: string | undefined = "hello";
  if (text !== undefined) {
    console.log(text.length);
  }
  text = undefined;
  if (text === undefined) {
    console.log(2);
  }

  console.log(label("abc"));
  console.log(label(undefined));
}

demo();
