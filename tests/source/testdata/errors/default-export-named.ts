// 默认导出不是命名导出：即使函数带名字，也不能被当成具名导出登记。
// 它走模块体骨架路径，由能力层按 cap.unsupported-syntax 拒绝。
export default function answer(): number {
  return 42;
}