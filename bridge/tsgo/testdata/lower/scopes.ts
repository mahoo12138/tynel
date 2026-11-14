// 调试作用域夹具：函数根、while 条件准备、循环体、if 两条分支各自一个作用域。
function count(n: number): number {
  let total: number = 1;
  let i: number = n;
  while (i > 0) {
    if (i === 1) {
      const x: number = 2;
      total = total + x;
    } else {
      const y: number = 3;
      total = total + y;
    }
    i = i - 1;
  }
  return total;
}
console.log(count(2));
