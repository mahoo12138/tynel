// 一元负号作用于变量：需要额外的取负操作，当前子集应在检查阶段拒绝。
function negate(value: number): number {
  return -value;
}

console.log(negate(3));
