// 合法 TypeScript：泛型函数的可选参数。当前泛型单态化不支持省略实参，
// 按 cap.unsupported-parameter 拒绝。
function opt<T>(value: T, fallback?: T): T {
  return value;
}

console.log(opt(1));
