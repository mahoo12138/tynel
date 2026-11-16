function show(): void {
  let count: number = 0;
  for (let i: number = 0; ; i = i + 1) {
    count = count + 1;
    if (count > 1) {
      break;
    }
  }
  console.log(count);
}

show();
