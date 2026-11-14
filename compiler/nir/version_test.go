package nir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

func TestCanReadSupportedVersions(t *testing.T) {
	if !CanRead(SchemaVersion) {
		t.Fatalf("当前版本 %d 应可读", SchemaVersion)
	}
	if !CanRead(1) || !CanRead(2) {
		t.Fatal("版本 1 与 2 应可迁移")
	}
	for _, version := range []int{-1, 0, 4, 99} {
		if CanRead(version) {
			t.Errorf("版本 %d 不应被接受", version)
		}
	}
}

func TestMigrateRejectsUnknownVersion(t *testing.T) {
	err := Migrate(&Module{SchemaVersion: SchemaVersion + 1})
	var d *diagnostic.Diagnostic
	if !errors.As(err, &d) || d.Code != "nir.version" {
		t.Fatalf("未知版本应报 nir.version，实际 %v", err)
	}
	if err := Migrate(nil); err == nil {
		t.Fatal("空模块应被拒绝")
	}
}

func TestDecodeRejectsUnknownVersion(t *testing.T) {
	data, err := EncodeBytes(module("main", mainWith()))
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{0, SchemaVersion + 1, 99} {
		raw := strings.Replace(string(data), fmt.Sprintf(`"schemaVersion": %d`, SchemaVersion), fmt.Sprintf(`"schemaVersion": %d`, version), 1)
		_, err := DecodeBytes([]byte(raw), DefaultLimits())
		var d *diagnostic.Diagnostic
		if !errors.As(err, &d) || d.Code != "nir.version" {
			t.Errorf("版本 %d 应报 nir.version，实际 %v", version, err)
		}
	}
}

// TestMigrateRecordsOriginalVersion 确认迁移前的版本被保留，且 v1 会升级到
// 当前版本并清空调试段。
func TestMigrateRecordsOriginalVersion(t *testing.T) {
	current := module("main", mainWith())
	if err := Migrate(current); err != nil {
		t.Fatal(err)
	}
	if current.OriginalSchemaVersion != SchemaVersion {
		t.Fatalf("当前版本应记为 %d，实际 %d", SchemaVersion, current.OriginalSchemaVersion)
	}

	old := module("main", mainWith())
	old.SchemaVersion = 1
	old.Debug = &DebugInfo{Functions: []DebugFunction{{Function: "main"}}}
	if err := Migrate(old); err != nil {
		t.Fatal(err)
	}
	if old.SchemaVersion != SchemaVersion || old.OriginalSchemaVersion != 1 {
		t.Fatalf("v1 应升级到 %d 并记录原始版本 1，实际 %d / %d", SchemaVersion, old.SchemaVersion, old.OriginalSchemaVersion)
	}
	if old.Debug != nil {
		t.Fatal("v1 迁移不应保留调试段")
	}
	if old.Types == nil || len(old.Types) != 0 {
		t.Fatalf("v1 迁移应得到空类型表，实际 %#v", old.Types)
	}

	v2 := module("main", mainWith())
	v2.SchemaVersion = 2
	if err := Migrate(v2); err != nil {
		t.Fatal(err)
	}
	if v2.SchemaVersion != SchemaVersion || v2.OriginalSchemaVersion != 2 {
		t.Fatalf("v2 应升级到 %d 并记录原始版本 2，实际 %d / %d", SchemaVersion, v2.SchemaVersion, v2.OriginalSchemaVersion)
	}
	if v2.Types == nil || len(v2.Types) != 0 {
		t.Fatalf("v2 迁移应得到空类型表，实际 %#v", v2.Types)
	}
}

func TestReplayV1Fixture(t *testing.T) {
	path := filepath.Join("testdata", "replay-v1.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeBytes(data, DefaultLimits())
	if err != nil {
		t.Fatalf("回放文件应能解码：%v", err)
	}
	if decoded.SchemaVersion != SchemaVersion {
		t.Fatalf("回放文件应迁移到当前版本 %d，实际 %d", SchemaVersion, decoded.SchemaVersion)
	}
	if decoded.OriginalSchemaVersion != 1 {
		t.Fatalf("回放文件应记录原始版本 1，实际 %d", decoded.OriginalSchemaVersion)
	}
	if decoded.Debug != nil {
		t.Fatal("版本 1 不应有调试段")
	}
	if err := Verify(decoded); err != nil {
		t.Fatalf("回放文件应通过验证：%v", err)
	}
	encoded, err := EncodeBytes(decoded)
	if err != nil {
		t.Fatal(err)
	}
	again, err := DecodeBytes(encoded, DefaultLimits())
	if err != nil {
		t.Fatalf("重新编码的文件应能解码：%v", err)
	}
	if !equalModules(decoded, again) {
		t.Fatalf("回放前后的结构不一致：\n第一次 %#v\n第二次 %#v", decoded, again)
	}
}

// TestReplayV2DebugFixture 固定带调试段的版本 2 文件，确认作用域和局部变量
// 能穿过编解码与验证，且不影响执行核心。
func TestReplayV2DebugFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "replay-v2-debug.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeBytes(data, DefaultLimits())
	if err != nil {
		t.Fatalf("调试回放文件应能解码：%v", err)
	}
	if decoded.SchemaVersion != SchemaVersion || decoded.OriginalSchemaVersion != 2 {
		t.Fatalf("调试回放文件应迁移到 %d 并记录原始版本 2，实际 %d / %d", SchemaVersion, decoded.SchemaVersion, decoded.OriginalSchemaVersion)
	}
	if decoded.Functions[0].Result != TypeNumber || decoded.Functions[0].Params[0].Type != TypeNumber {
		t.Fatalf("v2 的字符串类型应映射为 ID：%+v", decoded.Functions[0])
	}
	if got := decoded.Debug.Functions[0].Locals[0].Type; got != TypeNumber {
		t.Fatalf("v2 调试局部类型应映射为 ID %d，实际 %d", TypeNumber, got)
	}
	if decoded.Debug == nil || len(decoded.Debug.Functions) != 1 {
		t.Fatalf("调试回放文件应包含一个函数的调试信息：%+v", decoded.Debug)
	}
	if err := Verify(decoded); err != nil {
		t.Fatalf("调试回放文件应通过验证：%v", err)
	}
	encoded, err := EncodeBytes(decoded)
	if err != nil {
		t.Fatal(err)
	}
	again, err := DecodeBytes(encoded, DefaultLimits())
	if err != nil {
		t.Fatalf("重新编码的调试文件应能解码：%v", err)
	}
	if !equalModules(decoded, again) {
		t.Fatal("调试回放文件的编解码结构不一致")
	}
}
