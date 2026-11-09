// 合法 TypeScript：匿名默认导出函数没有名字。前端事实提取不能对它解引用崩溃，
// 默认导出也不在子集内，能力层按 cap.unsupported-syntax 明确拒绝。
export default function (): number {
  return 42;
}