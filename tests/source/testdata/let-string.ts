// 局部 let 的 string 类型、拼接与写回。
function f(): string {
  let x: string = "a";
  x = x + "b";
  return x;
}

console.log(f());
