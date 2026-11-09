// 二元运算两侧都带准备语句时，左侧最终值必须在右侧准备之前物化。
// identity 会打印 10；如果它被推迟到右侧三元式之后才调用，
// 加法的输出就会从 1/10/3/4 变成 1/3/10/4，比较同理。
function mark(n: number): number {
  console.log(n);
  return n;
}

function identity(n: number): number {
  console.log(10);
  return n;
}

function add(flag: boolean): number {
  return identity(flag ? mark(1) : mark(2)) + (flag ? mark(3) : mark(4));
}

function less(flag: boolean): boolean {
  return identity(flag ? mark(5) : mark(6)) < (flag ? mark(7) : mark(8));
}

console.log(add(true));
console.log(less(true));
console.log(add(false));
console.log(less(false));
