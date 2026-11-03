package diagnostic

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestCategoryExitCodes(t *testing.T) {
	cases := []struct {
		category Category
		exit     int
		name     string
	}{
		{Source, 1, "源码错误"},
		{Usage, 2, "用法错误"},
		{Capability, 3, "能力限制"},
		{Environment, 4, "环境缺失"},
		{Internal, 5, "内部故障"},
	}
	for _, c := range cases {
		if got := c.category.ExitCode(); got != c.exit {
			t.Errorf("%v 的退出状态为 %d，期望 %d", c.category, got, c.exit)
		}
		if got := c.category.String(); got != c.name {
			t.Errorf("类别 %d 的名称为 %q，期望 %q", c.category, got, c.name)
		}
	}
}

func TestExitCodeMapsErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"空错误", nil, ExitOK},
		{"普通错误按内部故障", errors.New("boom"), ExitInternal},
		{"诊断按类别", New(Capability, "cap.unsupported", "暂不支持"), ExitCapability},
		{"包装后的诊断仍可识别", fmt.Errorf("上层包装: %w", New(Environment, "tool.missing", "缺工具")), ExitEnvironment},
	}
	for _, c := range cases {
		if got := ExitCode(c.err); got != c.want {
			t.Errorf("%s：退出状态为 %d，期望 %d", c.name, got, c.want)
		}
	}
}

func TestWriteToFormat(t *testing.T) {
	var buf bytes.Buffer
	d := New(Environment, "tool.missing", "找不到工具 %s", "go").
		At("examples/add.ts", 3, 5).
		WithDetail("line1\nline2\n")
	if err := Write(&buf, d); err != nil {
		t.Fatalf("Write 失败：%v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"tynel: 环境缺失 (tool.missing): 找不到工具 go",
		"  阶段: toolchain",
		"  位置: examples/add.ts:3:5",
		"  原因: line1\n    line2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("输出缺少 %q，实际为：\n%s", want, out)
		}
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("输出应以换行结束：%q", out)
	}
}

func TestWriteToWithoutPosition(t *testing.T) {
	var buf bytes.Buffer
	d := New(Usage, "", "缺少命令")
	if _, err := d.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo 失败：%v", err)
	}
	out := buf.String()
	if strings.Contains(out, "位置") {
		t.Errorf("没有位置时不应输出位置行：%q", out)
	}
	if !strings.Contains(out, "tynel: 用法错误: 缺少命令") {
		t.Errorf("没有代码时不应输出括号：%q", out)
	}
}

func TestWriteWrapsPlainError(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, errors.New("boom")); err != nil {
		t.Fatalf("Write 失败：%v", err)
	}
	if got := buf.String(); !strings.Contains(got, "内部故障") || !strings.Contains(got, "boom") {
		t.Errorf("普通错误应包装为内部故障并保留原因，实际为：%q", got)
	}
}

func TestStageFromCodeAndOverride(t *testing.T) {
	cases := []struct {
		code  string
		stage string
	}{
		{"cli.bad-flag", StageCLI},
		{"project.missing", StageProject},
		{"module.missing-export", StageModules},
		{"cap.bad-type", StageCapability},
		{"facts.decode", StageFrontend},
		{"host.failed", StageFrontend},
		{"build.output-unwritable", StageBuild},
		{"tool.failed", StageToolchain},
		{"run.exec-failed", StageRun},
		{"target.unsupported", StageTarget},
		{"unknown.code", StageInternal},
	}
	for _, c := range cases {
		if got := New(Source, c.code, "x").Stage; got != c.stage {
			t.Errorf("%s 的阶段为 %q，期望 %q", c.code, got, c.stage)
		}
	}
	if got := New(Internal, "tool.failed", "x").WithStage(StageBackend).Stage; got != StageBackend {
		t.Errorf("显式阶段没有覆盖默认值：%q", got)
	}
}

func TestJSONMatchesHumanDiagnostic(t *testing.T) {
	d := New(Source, "TS2322", "Type 'string' is not assignable to type 'number'.").
		At("src/main.ts", 2, 7).
		WithSeverity("error").
		WithStage(StageFrontend).
		WithDetail("child stderr")

	var human, machine bytes.Buffer
	if err := Write(&human, d); err != nil {
		t.Fatalf("写人类诊断失败：%v", err)
	}
	if err := WriteJSON(&machine, d); err != nil {
		t.Fatalf("写 JSON 诊断失败：%v", err)
	}
	for _, want := range []string{"源码错误", "TS2322", "frontend", "src/main.ts:2:7", "child stderr"} {
		if !strings.Contains(human.String(), want) {
			t.Errorf("人类输出缺少 %q：%s", want, human.String())
		}
	}

	var envelope Envelope
	if err := json.Unmarshal(machine.Bytes(), &envelope); err != nil {
		t.Fatalf("JSON 诊断无法解析：%v\n%s", err, machine.String())
	}
	if envelope.SchemaVersion != 1 || envelope.OK || envelope.ExitCode != ExitSource {
		t.Errorf("信封头部不对：%+v", envelope)
	}
	if len(envelope.Diagnostics) != 1 {
		t.Fatalf("诊断条数为 %d，期望 1", len(envelope.Diagnostics))
	}
	item := envelope.Diagnostics[0]
	if item.Category != "source" || item.CategoryName != "源码错误" || item.Code != "TS2322" || item.Stage != StageFrontend || item.Severity != "error" {
		t.Errorf("机器诊断字段不对：%+v", item)
	}
	if item.File != "src/main.ts" || item.Line != 2 || item.Column != 7 || item.Detail != "child stderr" {
		t.Errorf("机器诊断位置或原因不对：%+v", item)
	}
}

func TestWriteItemsKeepsSuccessEnvelope(t *testing.T) {
	var buf bytes.Buffer
	items := []*Diagnostic{New(Source, "TS6133", "声明未被使用").WithSeverity("warning").WithStage(StageFrontend)}
	if err := WriteItems(&buf, items, ExitOK); err != nil {
		t.Fatalf("写 JSON 诊断失败：%v", err)
	}
	var envelope Envelope
	if err := json.Unmarshal(buf.Bytes(), &envelope); err != nil {
		t.Fatalf("JSON 诊断无法解析：%v", err)
	}
	if !envelope.OK || envelope.ExitCode != ExitOK {
		t.Errorf("只有警告时信封不应写成失败：%+v", envelope)
	}
	if len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Severity != "warning" {
		t.Errorf("警告条目丢失：%+v", envelope)
	}
}

func TestWriteJSONWrapsPlainError(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, errors.New("boom")); err != nil {
		t.Fatalf("WriteJSON 失败：%v", err)
	}
	var envelope Envelope
	if err := json.Unmarshal(buf.Bytes(), &envelope); err != nil {
		t.Fatalf("JSON 诊断无法解析：%v", err)
	}
	if len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Category != "internal" || envelope.Diagnostics[0].Stage != StageInternal {
		t.Errorf("普通错误应包装为内部故障：%+v", envelope)
	}
}

func TestWriteNilErrorWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, nil); err != nil {
		t.Fatalf("Write 失败：%v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("空错误不应输出内容，实际为：%q", buf.String())
	}
}
