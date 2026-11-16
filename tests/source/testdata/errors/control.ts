// do...while 仍不在本阶段子集内。
function count(n: number): number {
  let i: number = 0;
  do {
    i = i + 1;
  } while (i < n);
  return i;
}

console.log(count(3));
