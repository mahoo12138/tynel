// 标准库全局函数的声明文件也会被前端算成「模块」，但没有对应的项目导入。
// 它必须在能力检查阶段按 cap.unknown-callee 拒绝，而不是等降低报内部故障。
console.log(isFinite(1));
