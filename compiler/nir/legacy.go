package nir

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// 版本 1 和 2 的类型字段是字符串。把这些文件解码成当前结构之前，先读进一组
// 只用于旧格式的镜像类型，再显式换成类型 ID。这样当前 Module 可以保持整数
// ID，旧格式也不会悄悄按新语义解释。
type legacyModule struct {
	SchemaVersion int              `json:"schemaVersion"`
	Entry         string           `json:"entry"`
	Functions     []legacyFunction `json:"functions"`
	Debug         *legacyDebug     `json:"debug"`
}

type legacyFunction struct {
	Name   string        `json:"name"`
	Params []legacyParam `json:"params"`
	Result string        `json:"result"`
	Body   []legacyStmt  `json:"body"`
	Pos    *Pos          `json:"pos"`
}

type legacyParam struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Pos  *Pos   `json:"pos"`
}

// legacyStmt 只在 Type 字段上不同；其余字段与当前 Stmt 一致。
type legacyStmt struct {
	Op        string       `json:"op"`
	Name      string       `json:"name"`
	Value     *Expr        `json:"value"`
	Cond      *Expr        `json:"cond"`
	Then      []legacyStmt `json:"then"`
	Else      []legacyStmt `json:"else"`
	Operator  string       `json:"operator"`
	Left      *Expr        `json:"left"`
	Right     []legacyStmt `json:"right"`
	ThenValue *Expr        `json:"thenValue"`
	ElseValue *Expr        `json:"elseValue"`
	Type      string       `json:"type"`
	Mutable   bool         `json:"mutable"`
	CondPre   []legacyStmt `json:"condPre"`
	Body      []legacyStmt `json:"body"`
	ID        int          `json:"id"`
	Target    int          `json:"target"`
	Pos       *Pos         `json:"pos"`
}

type legacyDebug struct {
	Functions []legacyDebugFunction `json:"functions"`
}

type legacyDebugFunction struct {
	Function string             `json:"function"`
	Scopes   []DebugScope       `json:"scopes"`
	Locals   []legacyDebugLocal `json:"locals"`
}

type legacyDebugLocal struct {
	Name       string `json:"name"`
	SourceName string `json:"sourceName"`
	Kind       string `json:"kind"`
	Type       string `json:"type"`
	Scope      int    `json:"scope"`
	Pos        *Pos   `json:"pos"`
	Mutable    bool   `json:"mutable"`
}

// decodeLegacy 解码版本 1 或 2 的文件并转换到当前结构。调用方已经确认版本
// 可读；这里仍按版本处理 v1 的调试段清空。
func decodeLegacy(data []byte, version int) (*Module, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var old legacyModule
	if err := dec.Decode(&old); err != nil {
		return nil, malformed(err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, badf("nir.trailing", "JSON 文档结束后还有多余内容")
	}
	module, err := old.toModule()
	if err != nil {
		return nil, err
	}
	// 以头部版本为准：旧格式的 schemaVersion 字段必须与解码入口看到的一致。
	module.SchemaVersion = version
	module.OriginalSchemaVersion = version
	if err := Migrate(module); err != nil {
		return nil, err
	}
	return module, nil
}

func (old *legacyModule) toModule() (*Module, error) {
	module := &Module{
		SchemaVersion: old.SchemaVersion,
		Types:         []TypeDef{},
		Entry:         old.Entry,
	}
	for i := range old.Functions {
		fn, err := old.Functions[i].toFunction()
		if err != nil {
			return nil, err
		}
		module.Functions = append(module.Functions, *fn)
	}
	if old.Debug != nil {
		debug := &DebugInfo{}
		for i := range old.Debug.Functions {
			fn := &old.Debug.Functions[i]
			out := DebugFunction{Function: fn.Function, Scopes: fn.Scopes}
			for j := range fn.Locals {
				local := &fn.Locals[j]
				typ, err := legacyTypeID(local.Type)
				if err != nil {
					return nil, err
				}
				out.Locals = append(out.Locals, DebugLocal{
					Name:       local.Name,
					SourceName: local.SourceName,
					Kind:       local.Kind,
					Type:       typ,
					Scope:      local.Scope,
					Pos:        local.Pos,
					Mutable:    local.Mutable,
				})
			}
			debug.Functions = append(debug.Functions, out)
		}
		module.Debug = debug
	}
	return module, nil
}

func (old *legacyFunction) toFunction() (*Function, error) {
	result, err := legacyTypeID(old.Result)
	if err != nil {
		return nil, err
	}
	fn := &Function{Name: old.Name, Result: result, Pos: old.Pos}
	for i := range old.Params {
		param, err := legacyTypeID(old.Params[i].Type)
		if err != nil {
			return nil, err
		}
		fn.Params = append(fn.Params, Param{Name: old.Params[i].Name, Type: param, Pos: old.Params[i].Pos})
	}
	body, err := legacyStmts(old.Body)
	if err != nil {
		return nil, err
	}
	fn.Body = body
	return fn, nil
}

func legacyStmts(stmts []legacyStmt) ([]Stmt, error) {
	if len(stmts) == 0 {
		return nil, nil
	}
	out := make([]Stmt, len(stmts))
	for i := range stmts {
		stmt, err := stmts[i].toStmt()
		if err != nil {
			return nil, err
		}
		out[i] = *stmt
	}
	return out, nil
}

func (old *legacyStmt) toStmt() (*Stmt, error) {
	stmt := &Stmt{
		Op:        old.Op,
		Name:      old.Name,
		Value:     old.Value,
		Cond:      old.Cond,
		Operator:  old.Operator,
		Left:      old.Left,
		ThenValue: old.ThenValue,
		ElseValue: old.ElseValue,
		Mutable:   old.Mutable,
		ID:        old.ID,
		Target:    old.Target,
		Pos:       old.Pos,
	}
	if old.Type != "" {
		typ, err := legacyTypeID(old.Type)
		if err != nil {
			return nil, err
		}
		stmt.Type = typ
	}
	var err error
	if stmt.Then, err = legacyStmts(old.Then); err != nil {
		return nil, err
	}
	if stmt.Else, err = legacyStmts(old.Else); err != nil {
		return nil, err
	}
	if stmt.Right, err = legacyStmts(old.Right); err != nil {
		return nil, err
	}
	if stmt.CondPre, err = legacyStmts(old.CondPre); err != nil {
		return nil, err
	}
	if stmt.Body, err = legacyStmts(old.Body); err != nil {
		return nil, err
	}
	return stmt, nil
}

// legacyTypeID 把版本 1/2 的类型名换成内置类型 ID。旧格式没有复合类型。
func legacyTypeID(name string) (int, error) {
	switch name {
	case "number":
		return TypeNumber, nil
	case "bool":
		return TypeBool, nil
	case "void":
		return TypeVoid, nil
	}
	return 0, badf("nir.bad-value", "旧格式类型 %q 只接受 number、bool 或 void", name)
}
