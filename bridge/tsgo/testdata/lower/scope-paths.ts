// 作用域 path 的复杂夹具：嵌套 else if、嵌套 while、短路和三元式准备块。
function classify(n: number): number {
  let total: number = 0;
  let i: number = n;
  while (i > 0) {
    if (i === 1) {
      const a: number = 1;
      total = total + a;
    } else if (i === 2) {
      const b: number = 2;
      total = total + b;
    } else {
      while (i > 3) {
        const c: number = 3;
        total = total + c;
        i = i - 1;
      }
      if (i === 4) {
        const d: number = 4;
        total = total + d;
      }
    }
    i = i - 1;
  }
  return total;
}

function choose(a: boolean, b: boolean, c: boolean): number {
  const x: number = a && b ? 1 : 2;
  const y: number = b || c ? 3 : 4;
  if (a) {
    const x: number = 9;
    console.log(x);
  }
  return x + y;
}

console.log(classify(2) + choose(true, false, true));
