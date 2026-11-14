// ambient 声明没有运行时实现：函数没有函数体，常量没有初始化表达式。
declare function hostValue(n: number): number;
declare const answer: number;

console.log(hostValue(1));
console.log(answer);