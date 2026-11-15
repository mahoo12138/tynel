//go:build tynelapp

// Package main 是 Tynel 的固定支持文件。
//
// 它只提供 NIR 无法表达、也不该由用户书写的最小宿主支持：把 number 输出到
// 标准输出。用户函数不在这里，它们由 bridge 从 NIR 构造成 IR 节点后加入同一
// 个包。这个文件对所有输入都相同，不随 NIR 变化。
package main

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"unicode/utf16"
	"unsafe"
)

// tynelString 是 Tynel 字符串的 Runtime 表示：UTF-16 code unit 序列。
//
// data 指向一个由 GC 管理的 uint16 数组，length 是 code unit 数。空字符串的
// data 为 nil、length 为 0。打印和 I/O 边界再转换成 UTF-8；用户代码不能直接
// 观察这层存储。
type tynelString struct {
	data   *uint16
	length int32
}

// units 返回字符串的只读视图。调用方不得修改返回的切片。
func (s tynelString) units() []uint16 {
	if s.length == 0 || s.data == nil {
		return nil
	}
	return unsafe.Slice(s.data, int(s.length))
}

// tynelStringFromUnits 接管一份新分配的 code unit 数组。调用后调用方不能再
// 修改它。
func tynelStringFromUnits(units []uint16) tynelString {
	if len(units) == 0 {
		return tynelString{}
	}
	return tynelString{data: unsafe.SliceData(units), length: int32(len(units))}
}

// tynelStringFromUTF8 把源码或 I/O 边界的 UTF-8 文本转成 UTF-16 存储。
// 非法 UTF-8 和孤立代理项不是合法源码字面量，转换时由 Go 的 rune 处理。
func tynelStringFromUTF8(v string) tynelString {
	return tynelStringFromUnits(utf16.Encode([]rune(v)))
}

// tynelStringConcat 按 code unit 拼接两个字符串，结果拥有独立存储。
func tynelStringConcat(a, b tynelString) tynelString {
	units := make([]uint16, int(a.length)+int(b.length))
	copy(units, a.units())
	copy(units[int(a.length):], b.units())
	return tynelStringFromUnits(units)
}

// tynelStringLen 返回 UTF-16 code unit 数。JavaScript 的 string.length 与它
// 一致，而不是 UTF-8 字节数或 Unicode 码点数。
func tynelStringLen(s tynelString) float64 {
	return float64(s.length)
}

// tynelStringEqual 按 code unit 序列比较内容。
func tynelStringEqual(a, b tynelString) bool {
	if a.length != b.length {
		return false
	}
	x, y := a.units(), b.units()
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// tynelPrintString 在打印边界把 UTF-16 转回 UTF-8，每次写一行。
func tynelPrintString(s tynelString) {
	fmt.Fprintln(os.Stdout, string(utf16.Decode(s.units())))
}

// tynelNumberFromBits 按位构造一个 number。go/constant 表示不了 NaN、
// 无穷和负零，bridge 对这三类值改用它，避免位模式在编译期被改写。
func tynelNumberFromBits(bits uint64) float64 {
	return math.Float64frombits(bits)
}

// tynelFormatNumber 把 binary64 写成一行文本。
//
// 有限值用最短且可回读的十进制写法（strconv 的 'g' 格式、精度 -1），所以
// 0.1 + 0.2 输出 0.30000000000000004，而不是四舍五入后的 0.3。四个特殊值
// 用固定拼写，避免不同格式化路径给出不同结果。
//
// 这套拼写是本阶段自定的，不是 JavaScript console.log 的协议：那边无穷写作
// Infinity，负零也写作 -0，而这里的 +Inf / -Inf 沿用 Go 的写法。接上完整的
// 数值语义时（03.x）会一并改。
func tynelFormatNumber(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case v == 0 && math.Signbit(v):
		return "-0"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// tynelPrintFloat64 是 NIR 里 print 语句的目标。
//
// 每次调用写一行，行尾一个换行符。它是受限输出，不是 Node 的 console：
// 只接受一个 number，没有格式化占位符，也不写标准错误。
func tynelPrintFloat64(v float64) {
	fmt.Fprintln(os.Stdout, tynelFormatNumber(v))
}

// tynelPrintBool 输出 boolean。拼写固定为 true 与 false，与 Go 的默认格式
// 一致，但不依赖它——输出的拼写是编译器约定的一部分。
func tynelPrintBool(v bool) {
	if v {
		fmt.Fprintln(os.Stdout, "true")
		return
	}
	fmt.Fprintln(os.Stdout, "false")
}

// tynelExitData 是运行时数据错误的退出状态。编译失败使用 1–5，数据错误单独
// 占 6，避免把「程序运行到非法状态」和「源码/能力/环境/内部故障」混在一起。
const tynelExitData = 6

// tynelDataError 把运行时数据错误写到标准错误并退出。它不是 Go panic：
// 用户程序不打印栈，也不经过 recover；错误边界是进程状态。
func tynelDataError(message string) {
	fmt.Fprintf(os.Stderr, "tynel: 数据错误：%s\n", message)
	os.Exit(tynelExitData)
}

// tynelIndexToInt 把 number 下标转成整数。JSON/TypeScript 的数字是 binary64，
// 非整数、NaN、无穷和超出 int32 范围的下标都不是数组元素访问。
func tynelIndexToInt(v float64) int {
	if math.IsNaN(v) || math.IsInf(v, 0) || v != math.Trunc(v) || v < -2147483648 || v > 2147483647 {
		tynelDataError("数组索引 " + tynelFormatNumber(v) + " 不是有效整数")
	}
	return int(v)
}

// tynelCheckBounds 检查数组下标。越界是运行时数据错误，不返回 undefined，
// 也不沿用 Go 的 panic。
func tynelCheckBounds(index, length int) {
	if index < 0 || index >= length {
		tynelDataError("数组索引 " + strconv.Itoa(index) + " 越界；长度 " + strconv.Itoa(length))
	}
}
