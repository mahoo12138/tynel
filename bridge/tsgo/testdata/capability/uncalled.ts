// 这个函数没有被调用，但检查不靠可达性：里面的 switch 同样要拒绝。
function neverCalled(n: number): number {
  switch (n) {
    case 0:
      return 0;
    default:
      return n;
  }
}
