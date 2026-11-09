// 本文件属于 tyneladapter 包，和 adapter.go 一起被复制进上游源码树。
//
// 它直接拿原始字面量文本测 numericBits：真实前端里扫描器会先把文本归一化
// 成十进制，只有这里的直接调用能覆盖「原始写法进来」的路径。
package tyneladapter

import (
	"strings"
	"testing"
)

func TestNumericBits(t *testing.T) {
	cases := []struct {
		name string
		text string
		bits uint64
	}{
		{"十进制整数", "42", 0x4045000000000000},
		{"零", "0", 0x0000000000000000},
		{"小数", "0.5", 0x3fe0000000000000},
		{"指数", "1e21", 0x444b1ae4d6e2ef50},
		{"归一化指数", "1e+21", 0x444b1ae4d6e2ef50},
		{"超出 uint64 的十六进制", "0x10000000000000000", 0x43f0000000000000},
		{"大写前缀", "0X10000000000000000", 0x43f0000000000000},
		{"超出 uint64 的二进制", "0b1" + strings.Repeat("0", 64), 0x43f0000000000000},
		{"超出 uint64 的八进制", "0o2" + strings.Repeat("0", 21), 0x43f0000000000000},
		{"uint64 上界舍入到 2^64", "0xffffffffffffffff", 0x43f0000000000000},
		{"2^53+1 舍入到 2^53", "0x20000000000001", 0x4340000000000000},
		{"2^53+3 按最近偶数舍入", "0x20000000000003", 0x4340000000000002},
		{"十进制 2^53+1 同样舍入", "9007199254740993", 0x4340000000000000},
		{"53 位以内精确表示", "0x1fffffffffffff", 0x433fffffffffffff},
		{"十六进制上溢", "0x1" + strings.Repeat("0", 300), 0x7ff0000000000000},
		{"二进制上溢", "0b1" + strings.Repeat("0", 1024), 0x7ff0000000000000},
		{"十进制上溢", "1e400", 0x7ff0000000000000},
		{"归一化的无穷文本", "Infinity", 0x7ff0000000000000},
		{"十进制下溢", "1e-400", 0x0000000000000000},
		{"十六进制分隔符", "0x1_0000_0000_0000_0000", 0x43f0000000000000},
		{"十进制分隔符", "1_000_000", 0x412e848000000000},
		{"扫描器归一化文本", "18446744073709552000", 0x43f0000000000000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := numericBits(tc.text)
			if err != nil {
				t.Fatalf("numericBits(%q) 报错：%v", tc.text, err)
			}
			if got != tc.bits {
				t.Fatalf("numericBits(%q) = 0x%016x，期望 0x%016x", tc.text, got, tc.bits)
			}
		})
	}
}

func TestNumericBitsRejectsInvalidText(t *testing.T) {
	for _, text := range []string{"abc", "0x", "0xzz", "1e"} {
		if _, err := numericBits(text); err == nil {
			t.Errorf("numericBits(%q) 应报错", text)
		}
	}
}
