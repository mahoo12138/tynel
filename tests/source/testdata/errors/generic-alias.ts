type Box<T> = { value: T };

function demo(): void {
  const box: Box<number> = { value: 1 };
  console.log(box.value);
}

demo();
