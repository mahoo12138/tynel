// TypeScript 接受 ambient 声明，但它没有函数实现。
declare function hostValue(n: number): number;

console.log(hostValue(1));