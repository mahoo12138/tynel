package abi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSnapshotMatchesFixture 固定 ABI v1 的布局快照。布局变化必须先提升 ABI
// 版本并更新这份夹具，不能悄悄改变既有字段。
func TestSnapshotMatchesFixture(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("testdata", "runtime-abi-v1.json"))
	if err != nil {
		t.Fatalf("读取布局快照失败：%v", err)
	}
	got, err := Encode()
	if err != nil {
		t.Fatalf("编码布局快照失败：%v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("布局快照与夹具不一致；布局变化需要提升 ABI 版本并更新夹具")
	}
	var snapshot Snapshot
	if err := json.Unmarshal(got, &snapshot); err != nil {
		t.Fatalf("布局快照不是合法 JSON：%v", err)
	}
	if snapshot.Version != Version || snapshot.Name != Name || snapshot.Target != Target {
		t.Fatalf("快照标识与常量不一致：%+v", snapshot)
	}
}

func TestSnapshotRecordsGCAndExitContract(t *testing.T) {
	snapshot := Current()
	if snapshot.GC.HiddenPointers == "" || !strings.Contains(snapshot.GC.HiddenPointers, "uintptr") {
		t.Fatalf("GC 契约应禁止用 uintptr 藏指针：%+v", snapshot.GC)
	}
	foundData := false
	for _, exit := range snapshot.Exits {
		if exit.Code == DataErrorExit && strings.Contains(exit.Meaning, "数据错误") {
			foundData = true
		}
	}
	if !foundData {
		t.Fatalf("错误边界应包含数据错误退出 %d：%+v", DataErrorExit, snapshot.Exits)
	}
	if snapshot.CrossModule == "" {
		t.Fatal("ABI 快照应说明跨模块布局")
	}
}

func TestVersionString(t *testing.T) {
	if got := VersionString(); got != "tynel-runtime-abi/1" {
		t.Fatalf("版本标识为 %q", got)
	}
}
