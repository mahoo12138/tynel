type Item = { value: number; label: string };

function make(value: number): Item {
  return { value: value, label: "item" };
}

function demo(): void {
  let i = 0;
  let total = 0;
  while (i < 1000) {
    const item = make(i);
    const box: Item[] = [item];
    const pair: [Item, number] = [box[0], i];
    const maybe: Item | null = pair[0];
    if (maybe !== null) {
      total = total + maybe.value + maybe.label.length;
    }
    i = i + 1;
  }
  console.log(total);
}

demo();
