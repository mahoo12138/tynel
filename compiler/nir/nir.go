// Package nir 定义 Tynel 的中间表示及其 JSON 文件格式。
//
// NIR 是前端与后端之间唯一的交接物：前端把源码理解成这个结构，后端只读这个
// 结构，双方不共享 tsgo 或 Go 的内部对象。本包只负责形状、编码和格式预算；
// 函数引用、参数数量和类型一致性由独立的验证器检查（01.04），不在这里顺手
// 做一半。
//
// 结构上它接近一个 C 结构体：每个节点有 op 标签，其余字段是标签决定用哪几个
// 的联合体。JSON 没有联合类型，所以同一层字段名固定，由 check 按 op 拒绝
// 不属于该节点的字段。
//
// codec.go 只管形状、编码和预算；verify.go 检查语义（入口签名、声明唯一性、
// 引用、实参、返回和类型）。两层都能独立拒绝坏输入，验证器不假设调用方一定
// 先做过 JSON 解码。
package nir

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// SchemaVersion 是当前文件格式版本。格式不兼容时递增，旧文件必须被拒绝，
// 不能靠缺省值兼容。版本 2 增加可选的 debug 段；版本 3 把类型字段从类型名
// 改成模块级类型表里的整数 ID。
const SchemaVersion = 3

// 内置类型 ID。它们由格式保留，不需要在 types 表里重复声明，也不能被覆盖。
// 复合类型从 FirstComposite 开始由模块的 types 表定义。
const (
	TypeNumber    = 1
	TypeBool      = 2
	TypeVoid      = 3
	TypeString    = 4
	TypeNull      = 5
	TypeUndefined = 6
	TypeNever     = 7

	FirstComposite = 8
)

// 表达式操作。
const (
	OpNumber  = "number"
	OpBool    = "bool"
	OpString  = "string"
	OpParam   = "param"
	OpLocal   = "local"
	OpCall    = "call"
	OpBinary  = "binary"
	OpConcat  = "concat"
	OpStrlen  = "strlen"
	OpLen     = "len"
	OpField   = "field"
	OpCompare = "compare"
	OpNot     = "not"
)

// 语句操作。
//
// let 定义一个局部值，供后续语句按名字读取。它不是语言层面的变量：前端把
// 模块级 const 和中间结果落到这里，避免在表达式里重复求值。同名重复定义和
// 先读后定义都由验证器拒绝。
//
// discard 求值一个表达式并丢弃结果。源码里 `f();` 和 `answer;` 都是这个形状：
// 调用可能带副作用，必须执行；值没人要，不能顺手打印出去。丢弃的是结果，
// 不是求值本身。
const (
	OpReturn   = "return"
	OpPrint    = "print"
	OpLet      = "let"
	OpDiscard  = "discard"
	OpIf       = "if"
	OpShort    = "short"
	OpSelect   = "select"
	OpSet      = "set"
	OpObject   = "object"
	OpArray    = "array"
	OpTuple    = "tuple"
	OpIndex    = "index"
	OpWhile    = "while"
	OpBreak    = "break"
	OpContinue = "continue"
)

// BinaryOperators 是首版允许的算术运算符，顺序用于报错提示。
var BinaryOperators = []string{"+", "-", "*", "/"}

// CompareOperators 是比较运算符。有序比较只接受 number，严格相等接受同类
// number 或 boolean。顺序用于报错提示。
var CompareOperators = []string{"<", "<=", ">", ">=", "===", "!=="}

// OrderingOperators 是需要两个 number 的有序比较。
var OrderingOperators = []string{"<", "<=", ">", ">="}

// EqualityOperators 是要求两侧同类型的严格相等。
var EqualityOperators = []string{"===", "!=="}

// Module 是一个可编译单元。
type Module struct {
	SchemaVersion int        `json:"schemaVersion"`
	Types         []TypeDef  `json:"types"`
	Entry         string     `json:"entry"`
	Functions     []Function `json:"functions"`
	Debug         *DebugInfo `json:"debug,omitempty"`

	// OriginalSchemaVersion 记录解码时读到的版本，迁移后保留。它不写进 JSON，
	// 只供构建器判断是否需要把迁移结果重新落盘。
	OriginalSchemaVersion int `json:"-"`
}

// Function 是具名函数。Result 是类型 ID；内置类型直接用 1..7，复合类型引用
// Module.Types 里的定义。
type Function struct {
	Name   string  `json:"name"`
	Params []Param `json:"params"`
	Result int     `json:"result"`
	Body   []Stmt  `json:"body"`
	Pos    *Pos    `json:"pos,omitempty"`
}

// Param 是函数参数。Type 是类型 ID，且不能是 void。
type Param struct {
	Name string `json:"name"`
	Type int    `json:"type"`
	Pos  *Pos   `json:"pos,omitempty"`
}

// Pos 是源码位置。手写 NIR 时可以省略，前端产出时填写。
type Pos struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// Stmt 是一条语句。Value 为 nil 只对无返回值的 return 合法；Cond、Then 与
// Else 属于 if 和 select；Operator、Left、Right 与 Value 只属于 short（Right
// 是右侧的准备工作，Value 是准备完之后的右值）；ThenValue 与 ElseValue 只属于
// select，是两条分支准备完之后各自的结果。Items 属于 array/tuple 字面量，
// Index 与 Value 属于 index 语句。
type Stmt struct {
	Op        string        `json:"op"`
	Name      string        `json:"name,omitempty"` // let、short、select、object、array、tuple 或 index 的结果名
	Value     *Expr         `json:"value,omitempty"`
	Cond      *Expr         `json:"cond,omitempty"` // if 或 select 使用
	Then      []Stmt        `json:"then,omitempty"`
	Else      []Stmt        `json:"else,omitempty"`
	Operator  string        `json:"operator,omitempty"`  // 仅 short 使用
	Left      *Expr         `json:"left,omitempty"`      // 仅 short 使用
	Right     []Stmt        `json:"right,omitempty"`     // 仅 short 使用
	ThenValue *Expr         `json:"thenValue,omitempty"` // 仅 select 使用
	ElseValue *Expr         `json:"elseValue,omitempty"` // 仅 select 使用
	Type      int           `json:"type,omitempty"`      // select、object、array、tuple 或 index 使用：结果类型 ID
	Fields    []ObjectField `json:"fields,omitempty"`    // 仅 object 使用，按源码顺序求值
	Items     []Expr        `json:"items,omitempty"`     // 仅 array/tuple 使用，按源码顺序求值
	Index     *Expr         `json:"index,omitempty"`     // 仅 index 使用
	Mutable   bool          `json:"mutable,omitempty"`   // 仅 let 使用：是否为可变的 let
	CondPre   []Stmt        `json:"condPre,omitempty"`   // 仅 while 使用：每轮条件准备
	Body      []Stmt        `json:"body,omitempty"`      // 仅 while 使用：循环体
	ID        int           `json:"id,omitempty"`        // 仅 while 使用：循环身份
	Target    int           `json:"target,omitempty"`    // 仅 break / continue 使用
	Pos       *Pos          `json:"pos,omitempty"`
}

// ObjectField 是 object 语句里的一个字段初始化。name 必须在目标对象类型里
// 存在，value 按字段在源码里的顺序求值。
type ObjectField struct {
	Name  string `json:"name"`
	Value Expr   `json:"value"`
}

// Expr 是一个表达式节点。字段按 Op 取用，其余字段必须为空。
//
// Value 是 bool 或 string 字面量的原始 JSON。bool 用 true/false 保存，这样
// false 与「字段缺失」是两种不同的输入；string 用 JSON 字符串保存，空字符串
// 同样是一个存在的值。用 RawMessage 而不是某个具体类型的指针，是因为同一个
// 字段名要承载两种 JSON 类型，而它们的合法性由 op 决定。
type Expr struct {
	Op       string          `json:"op"`
	Bits     string          `json:"bits,omitempty"`
	Value    json.RawMessage `json:"value,omitempty"`
	Name     string          `json:"name,omitempty"`
	Function string          `json:"function,omitempty"`
	Args     []Expr          `json:"args,omitempty"`
	Operator string          `json:"operator,omitempty"`
	Left     *Expr           `json:"left,omitempty"`
	Right    *Expr           `json:"right,omitempty"`
	Operand  *Expr           `json:"operand,omitempty"`
	Pos      *Pos            `json:"pos,omitempty"`
}

// BitsOf 返回 f 的 binary64 位模式，格式与文件中的 bits 字段一致。
func BitsOf(f float64) string {
	return fmt.Sprintf("0x%016x", math.Float64bits(f))
}

// Float64 把 bits 字段解析为 float64。它保留负零、NaN 和无穷的位模式。
func Float64(bits string) (float64, error) {
	v, err := parseBits(bits)
	if err != nil {
		return 0, err
	}
	return math.Float64frombits(v), nil
}

// Number 构造一个 number 字面量节点。
func Number(f float64) Expr {
	return Expr{Op: OpNumber, Bits: BitsOf(f)}
}

// Bool 构造一个 bool 字面量节点。false 也写出 value 字段，不会被当成缺字段。
func Bool(v bool) Expr {
	if v {
		return Expr{Op: OpBool, Value: json.RawMessage("true")}
	}
	return Expr{Op: OpBool, Value: json.RawMessage("false")}
}

// String 构造一个 string 字面量节点。空字符串会写成 ""，字段仍然存在。
func String(v string) Expr {
	data, _ := json.Marshal(v)
	return Expr{Op: OpString, Value: data}
}

// BoolValue 取出 bool 字面量的值。ok=false 表示字段缺失或不是 JSON 布尔。
func (e *Expr) BoolValue() (value, ok bool) {
	if e == nil || len(e.Value) == 0 {
		return false, false
	}
	if err := json.Unmarshal(e.Value, &value); err != nil {
		return false, false
	}
	return value, true
}

// StringValue 取出 string 字面量的值。ok=false 表示字段缺失或不是 JSON 字符串。
func (e *Expr) StringValue() (value string, ok bool) {
	if e == nil || len(e.Value) == 0 {
		return "", false
	}
	if err := json.Unmarshal(e.Value, &value); err != nil {
		return "", false
	}
	return value, true
}

// parseBits 要求 16 位十六进制，允许 0x 前缀，不区分大小写。
func parseBits(bits string) (uint64, error) {
	s := strings.TrimSpace(bits)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if len(s) != 16 {
		return 0, fmt.Errorf("bits %q 必须是 16 位十六进制", bits)
	}
	v, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("bits %q 不是合法的十六进制：%w", bits, err)
	}
	return v, nil
}

// IsBinaryOperator 报告 op 是否为支持的二元运算符。
func IsBinaryOperator(op string) bool {
	for _, candidate := range BinaryOperators {
		if candidate == op {
			return true
		}
	}
	return false
}

// IsCompareOperator 报告 op 是否为支持的比较运算符。
func IsCompareOperator(op string) bool {
	return IsOrderingOperator(op) || IsEqualityOperator(op)
}

// IsOrderingOperator 报告 op 是否是有序比较。
func IsOrderingOperator(op string) bool {
	for _, candidate := range OrderingOperators {
		if candidate == op {
			return true
		}
	}
	return false
}

// IsEqualityOperator 报告 op 是否是严格相等。
func IsEqualityOperator(op string) bool {
	for _, candidate := range EqualityOperators {
		if candidate == op {
			return true
		}
	}
	return false
}
