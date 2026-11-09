// 合法 TypeScript：b 是可选参数，调用时可以省略。当前阶段同样只支持必填
// 参数，按 cap.unsupported-parameter 拒绝。
function opt(a: number, b?: number): number {
  return a;
}
console.log(opt(1));
