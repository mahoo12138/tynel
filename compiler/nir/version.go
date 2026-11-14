package nir

// NIR 的 schemaVersion 是文件格式版本，不是语言版本或编译器版本。格式不兼容
// 时递增，旧文件必须显式迁移或被拒绝，不能靠缺省值猜测。版本规则集中在
// 本文件，解码器和编码器不各自维护一份判断。

// CanRead 报告解码器是否能读取某个 schemaVersion。它能读的版本要么就是当前
// 版本，要么有明确的迁移规则。
func CanRead(version int) bool {
	switch version {
	case 1, SchemaVersion:
		return true
	}
	return false
}

// Migrate 把已解码的模块迁移到当前 SchemaVersion。
//
// 它必须在格式检查和语义验证之前调用。版本 1 没有 debug 段，迁移只更新版本
// 号；未来的版本在这里写显式转换，而不是在 check 里加缺省值。
func Migrate(module *Module) error {
	if module == nil {
		return badf("nir.missing", "模块为空")
	}
	if module.OriginalSchemaVersion == 0 {
		module.OriginalSchemaVersion = module.SchemaVersion
	}
	if !CanRead(module.SchemaVersion) {
		return badf("nir.version", "schemaVersion 为 %d，本版本只接受 1..%d", module.SchemaVersion, SchemaVersion)
	}
	switch module.SchemaVersion {
	case 1:
		module.SchemaVersion = SchemaVersion
		module.Debug = nil
		return nil
	case SchemaVersion:
		return nil
	default:
		return badf("nir.version", "schemaVersion %d 还没有迁移到 %d 的规则", module.SchemaVersion, SchemaVersion)
	}
}
