// 合法 TypeScript：b 有默认值，调用时可以只给 a。事实层要接受这种调用，
// 能力层按 cap.unsupported-parameter 明确拒绝默认参数本身。
function add(a: number, b: number = 10): number {
  return a + b;
}
console.log(add(5));
