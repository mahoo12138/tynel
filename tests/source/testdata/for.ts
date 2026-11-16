function sum(n: number): number {
  let total: number = 0;
  for (let i: number = 0; i < n; i = i + 1) {
    total = total + i;
  }
  return total;
}

function skip(n: number): number {
  let total: number = 0;
  for (let i: number = 0; i < n; i = i + 1) {
    if (i === 2) {
      continue;
    }
    total = total + i;
  }
  return total;
}

function halt(n: number): number {
  let total: number = 0;
  for (let i: number = 0; i < n; i = i + 1) {
    if (i === 2) {
      break;
    }
    total = total + i;
  }
  return total;
}

function nested(n: number): number {
  let total: number = 0;
  for (let i: number = 0; i < n; i = i + 1) {
    for (let j: number = 0; j < i; j = j + 1) {
      if (j === 1) {
        continue;
      }
      total = total + 1;
    }
  }
  return total;
}

function shadow(): number {
  let total: number = 0;
  for (let i: number = 0; i < 3; i = i + 1) {
    total = total + i;
  }
  let i: number = 10;
  return total + i;
}

function countdown(n: number): number {
  let steps: number = 0;
  for (let i: number = n; i > 0; i = i - 1) {
    steps = steps + 1;
  }
  return steps;
}

function find(n: number): number {
  for (let i: number = 0; i < n; i = i + 1) {
    if (i === 3) {
      return i;
    }
  }
  return -1;
}

function once(): number {
  let total: number = 0;
  for (let i: number = 0; i < 1; ) {
    total = total + 5;
    break;
  }
  return total;
}

console.log(sum(5));
console.log(sum(0));
console.log(skip(5));
console.log(halt(5));
console.log(nested(4));
console.log(shadow());
console.log(countdown(3));
console.log(find(6));
console.log(find(2));
console.log(once());
