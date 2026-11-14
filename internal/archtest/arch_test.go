// Package archtest 把 Stage 1 的依赖方向写成可执行检查。
//
// 这些测试不验证业务行为，只验证包边界：上游 TypeScript 类型和 Go 编译器内部
// 对象不能穿过编译器边界，NIR 不能反向依赖前端，前端不能反向依赖降低器。
// 边界一旦被破坏，失败信息会直接指出是哪个包引用了哪个非法目标。
package archtest

import (
	"bytes"
	"encoding/json"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const modulePath = "github.com/Mahoo12138/tynel/"

type listedPackage struct {
	ImportPath string
	Imports    []string
	Dir        string
}

// listPackages 只列根模块的源码包，不列 out/、.work/ 里的构建产物。
func listPackages(t *testing.T) []listedPackage {
	t.Helper()
	cmd := exec.Command("go", "list", "-json", "./compiler/...", "./cmd/...", "./internal/...", "./tests/...")
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list 失败：%v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	var packages []listedPackage
	for {
		var p listedPackage
		err := dec.Decode(&p)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("解析 go list 输出失败：%v", err)
		}
		packages = append(packages, p)
	}
	if len(packages) == 0 {
		t.Fatal("go list 没有返回任何包")
	}
	return packages
}

func TestNoUpstreamTypesOutsideFrontendFacade(t *testing.T) {
	for _, p := range listPackages(t) {
		for _, imp := range p.Imports {
			if strings.HasPrefix(imp, "github.com/microsoft/typescript-go") {
				t.Errorf("%s 导入了 %s；上游 TypeScript 类型只能留在 bridge/tsgo 适配层", p.ImportPath, imp)
			}
		}
	}
}

func TestNoGoCompilerInternalsOutsideBridge(t *testing.T) {
	for _, p := range listPackages(t) {
		for _, imp := range p.Imports {
			if strings.HasPrefix(imp, "cmd/compile/internal") || strings.HasPrefix(imp, "cmd/internal") {
				t.Errorf("%s 导入了 %s；Go 编译器内部对象只能在 bridge/go 的合法桥接包内使用", p.ImportPath, imp)
			}
		}
	}
}

func TestCompilerLayerDirection(t *testing.T) {
	forbidden := map[string][]string{
		// NIR 是前后端唯一交接物，不能反向依赖前端事实、降低器或模块解析。
		modulePath + "compiler/nir": {
			modulePath + "compiler/frontend",
			modulePath + "compiler/lower",
			modulePath + "compiler/capability",
			modulePath + "compiler/merge",
			modulePath + "compiler/modules",
			modulePath + "compiler/graph",
			modulePath + "compiler/project",
		},
		// 前端事实不能依赖降低、IR 或模块图。
		modulePath + "compiler/frontend": {
			modulePath + "compiler/lower",
			modulePath + "compiler/nir",
			modulePath + "compiler/capability",
			modulePath + "compiler/merge",
			modulePath + "compiler/modules",
			modulePath + "compiler/graph",
			modulePath + "compiler/project",
		},
		// 能力检查是语义层的一步，不负责生成 NIR 或合并模块。
		modulePath + "compiler/capability": {
			modulePath + "compiler/lower",
			modulePath + "compiler/merge",
			modulePath + "compiler/modules",
			modulePath + "compiler/graph",
			modulePath + "compiler/project",
		},
		// 模块解析只认项目根与路径规则，不反向依赖依赖图或降低器。
		modulePath + "compiler/project": {
			modulePath + "compiler/graph",
			modulePath + "compiler/modules",
			modulePath + "compiler/merge",
			modulePath + "compiler/lower",
		},
	}
	for _, p := range listPackages(t) {
		for _, imp := range p.Imports {
			for _, bad := range forbidden[p.ImportPath] {
				if imp == bad {
					t.Errorf("%s 不能导入 %s；编译器层的依赖方向被反转了", p.ImportPath, imp)
				}
			}
		}
	}
}
