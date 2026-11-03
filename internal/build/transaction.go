package build

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

// publication 是一次产物与记录发布所需要的路径。
//
// 产物和记录是两个文件，文件系统没有把它们一起换掉的单个操作。这里用
// pending 标记表示「发布已经开始但还没有确认完成」，用备份保留上一对成功
// 文件；进程内失败立即回滚，进程被杀死后下一次构建先恢复。它不声称跨两个
// 文件的绝对原子性，只保证未完成的发布不会被当成成功。
type publication struct {
	output       string
	record       string
	stagedOutput string
	stagedRecord string
	backupOutput string
	backupRecord string
	pending      string
	lock         string
	// 本次是否已经把新的产物或记录改名到目标位置。回滚时不能把还没被
	// 替换的旧文件删掉。
	installedOutput bool
	installedRecord bool
}

func newPublication(output string) publication {
	dir := filepath.Dir(output)
	tag := short(sha256Hex([]byte(output)))
	prefix := filepath.Join(dir, ".tynel-"+tag)
	return publication{
		output:       output,
		record:       RecordPath(output),
		backupOutput: prefix + ".artifact.bak",
		backupRecord: prefix + ".record.bak",
		pending:      prefix + ".pending",
		lock:         prefix + ".lock",
	}
}

// pendingInfo 记录发布开始时目标位置是否已有旧文件。恢复不能只看备份：
// 如果旧文件还没被移走，目标位置上的就是旧文件，不能把它当成新文件删掉。
type pendingInfo struct {
	PID       int  `json:"pid"`
	HadOutput bool `json:"hadOutput"`
	HadRecord bool `json:"hadRecord"`
}

// recover 清理上一次没有完成的发布。调用方必须已经持有输出锁。
//
// 如果当前产物和记录已经互相对得上，就保留它们并只清掉备份；否则按
// pending 记录的状态恢复旧文件。没有 pending 也没有备份时什么也不做。
func (p publication) recover() error {
	pending, err := pathExists(p.pending)
	if err != nil {
		return environment("build.publish-failed", "无法检查发布标记 %s", p.pending).WithDetail(err.Error())
	}
	backup, err := pathExists(p.backupOutput)
	if err != nil {
		return environment("build.publish-failed", "无法检查产物备份 %s", p.backupOutput).WithDetail(err.Error())
	}
	recordBackup, err := pathExists(p.backupRecord)
	if err != nil {
		return environment("build.publish-failed", "无法检查记录备份 %s", p.backupRecord).WithDetail(err.Error())
	}
	if !pending && !backup && !recordBackup {
		return nil
	}
	if pending {
		info, readErr := readPending(p.pending)
		if readErr == nil {
			p.installedOutput = !info.HadOutput
			p.installedRecord = !info.HadRecord
		}
		// 标记损坏时保留没有备份的一侧，宁可留下未配对的文件，也不误删
		// 用户原有的产物或记录。
	} else {
		p.installedOutput = backup
		p.installedRecord = recordBackup
	}
	if VerifyPair(p.output) == nil {
		p.cleanup()
		return nil
	}
	return p.restore()
}

func readPending(path string) (pendingInfo, error) {
	var info pendingInfo
	data, err := os.ReadFile(path)
	if err != nil {
		return info, err
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return info, err
	}
	return info, nil
}

// commit 把已经写好的临时产物和临时记录发布到目标位置。
func (p publication) commit() error {
	if err := p.begin(); err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			_ = p.restore()
		}
	}()

	if err := p.moveOld(p.output, p.backupOutput, "build.output-unwritable", "无法备份旧产物"); err != nil {
		return err
	}
	if err := p.moveOld(p.record, p.backupRecord, "build.record-failed", "无法备份旧构建记录"); err != nil {
		return err
	}
	if err := os.Rename(p.stagedOutput, p.output); err != nil {
		return environment("build.output-unwritable", "无法发布到 %s", p.output).WithDetail(err.Error())
	}
	p.installedOutput = true
	if err := os.Rename(p.stagedRecord, p.record); err != nil {
		return environment("build.record-failed", "无法发布构建记录 %s", p.record).WithDetail(err.Error())
	}
	p.installedRecord = true
	if err := VerifyPair(p.output); err != nil {
		return err
	}
	done = true
	p.cleanup()
	return nil
}

func (p publication) begin() error {
	hadOutput, err := pathExists(p.output)
	if err != nil {
		return environment("build.publish-failed", "无法检查旧产物 %s", p.output).WithDetail(err.Error())
	}
	hadRecord, err := pathExists(p.record)
	if err != nil {
		return environment("build.publish-failed", "无法检查旧构建记录 %s", p.record).WithDetail(err.Error())
	}
	data, err := json.Marshal(pendingInfo{PID: os.Getpid(), HadOutput: hadOutput, HadRecord: hadRecord})
	if err != nil {
		return environment("build.publish-failed", "无法序列化发布标记").WithDetail(err.Error())
	}
	data = append(data, '\n')
	f, err := os.OpenFile(p.pending, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return environment("build.publish-pending", "无法开始发布 %s；上一次发布可能需要恢复", p.output).WithDetail(err.Error())
	}
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		os.Remove(p.pending)
		return environment("build.publish-failed", "无法写入发布标记 %s", p.pending).WithDetail(writeErr.Error())
	}
	if closeErr != nil {
		os.Remove(p.pending)
		return environment("build.publish-failed", "无法关闭发布标记 %s", p.pending).WithDetail(closeErr.Error())
	}
	return nil
}

func (p publication) moveOld(from, to, code, message string) error {
	exists, err := pathExists(from)
	if err != nil {
		return environment(code, "%s：%s", message, from).WithDetail(err.Error())
	}
	if !exists {
		return nil
	}
	removeFile(to)
	if err := os.Rename(from, to); err != nil {
		return environment(code, "%s：%s", message, from).WithDetail(err.Error())
	}
	return nil
}

// restore 回到上一对成功文件。没有旧产物时，本次刚写出的部分也要删掉，
// 不能留下没有记录的产物。
func (p publication) restore() error {
	var errs []error
	if exists, err := pathExists(p.backupOutput); err != nil {
		errs = append(errs, err)
	} else if exists {
		removeFile(p.output)
		if err := os.Rename(p.backupOutput, p.output); err != nil {
			errs = append(errs, fmt.Errorf("恢复旧产物 %s：%w", p.output, err))
		}
	} else if p.installedOutput {
		removeFile(p.output)
	}
	if exists, err := pathExists(p.backupRecord); err != nil {
		errs = append(errs, err)
	} else if exists {
		removeFile(p.record)
		if err := os.Rename(p.backupRecord, p.record); err != nil {
			errs = append(errs, fmt.Errorf("恢复旧构建记录 %s：%w", p.record, err))
		}
	} else if p.installedRecord {
		removeFile(p.record)
	}
	removeFile(p.pending)
	if len(errs) > 0 {
		return environment("build.rollback-failed", "发布失败，恢复旧产物时又遇到错误").WithDetail(errors.Join(errs...).Error())
	}
	return nil
}

func (p publication) cleanup() {
	removeFile(p.pending)
	removeFile(p.backupOutput)
	removeFile(p.backupRecord)
}

// VerifyPair 复核产物与构建记录是否描述同一份内容。记录存在但哈希对不上
// 时不能把这次构建当成成功；它可能是被中断的发布或外部改动。
func VerifyPair(output string) error {
	recordPath := RecordPath(output)
	data, err := os.ReadFile(recordPath)
	if err != nil {
		return environment("build.record-missing", "找不到构建记录 %s", recordPath).WithDetail(err.Error())
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return environment("build.record-corrupt", "构建记录 %s 无法解析", recordPath).WithDetail(err.Error())
	}
	if record.SchemaVersion != 1 {
		return environment("build.record-corrupt", "构建记录 %s 的 schemaVersion 为 %d", recordPath, record.SchemaVersion)
	}
	if len(record.Output.SHA256) != 64 {
		return environment("build.record-corrupt", "构建记录 %s 没有产物内容哈希", recordPath)
	}
	info, err := os.Stat(output)
	if err != nil {
		return environment("build.output-missing", "构建记录存在，但产物 %s 不可读", output).WithDetail(err.Error())
	}
	if info.IsDir() || info.Size() != record.Output.Bytes {
		return environment("build.record-mismatch", "产物 %s 的大小与构建记录不一致", output)
	}
	got, err := hashFile(output)
	if err != nil {
		return environment("build.output-unreadable", "无法读取构建产物 %s", output).WithDetail(err.Error())
	}
	if got != record.Output.SHA256 {
		return environment("build.record-mismatch", "产物 %s 的内容与构建记录不一致", output)
	}
	return nil
}

// stageRecord 把记录写到目标目录的临时文件，返回待发布路径。
func stageRecord(recordPath string, record *Record) (string, error) {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", diagnostic.New(diagnostic.Internal, "build.record-failed", "无法序列化构建记录").WithDetail(err.Error())
	}
	data = append(data, '\n')
	staged, err := stageBytes(filepath.Dir(recordPath), ".tynel-record-*", data, 0o644)
	if err != nil {
		return "", environment("build.record-failed", "无法写入构建记录 %s", recordPath).WithDetail(err.Error())
	}
	return staged, nil
}

// ensureSnapshot 按内容哈希放置 NIR 快照。内容不符的旧文件直接拒绝；
// 写入先落临时文件，避免并发构建读到写了一半的快照。
func ensureSnapshot(path string, data []byte, sha string) error {
	if existing, err := os.ReadFile(path); err == nil {
		if sha256Hex(existing) != sha {
			return environment("build.snapshot-corrupt", "输入快照 %s 的内容与文件名不符", path)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return environment("build.snapshot-failed", "无法读取输入快照 %s", path).WithDetail(err.Error())
	}
	if err := writeAtomic(path, data, 0o644); err != nil {
		return environment("build.snapshot-failed", "无法写入输入快照 %s", path).WithDetail(err.Error())
	}
	got, err := hashFile(path)
	if err != nil {
		return environment("build.snapshot-failed", "无法复核输入快照 %s", path).WithDetail(err.Error())
	}
	if got != sha {
		return environment("build.snapshot-corrupt", "输入快照 %s 写入后内容不符", path)
	}
	return nil
}

// writeAtomic 用同目录临时文件加重命名写入一个内容固定的文件。
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	staged, err := stageBytes(filepath.Dir(path), ".tynel-tmp-*", data, perm)
	if err != nil {
		return err
	}
	if err := os.Rename(staged, path); err != nil {
		os.Remove(staged)
		return err
	}
	return nil
}

func stageBytes(dir, pattern string, data []byte, perm os.FileMode) (string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	name := f.Name()
	failed := func(cause error) (string, error) {
		f.Close()
		os.Remove(name)
		return "", cause
	}
	if err := f.Chmod(perm); err != nil {
		return failed(err)
	}
	if _, err := f.Write(data); err != nil {
		return failed(err)
	}
	if err := f.Sync(); err != nil {
		return failed(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

func syncFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func pathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// removeFile 只删除文件或空目录。用户可能把输出路径或记录路径占成目录，
// 回滚不能递归删除它。
func removeFile(path string) {
	info, err := os.Lstat(path)
	if err != nil || info.IsDir() {
		return
	}
	_ = os.Remove(path)
}

func environment(code, format string, args ...any) *diagnostic.Diagnostic {
	stage := diagnostic.StagePublish
	switch code {
	case "build.snapshot-failed", "build.snapshot-corrupt":
		stage = diagnostic.StageBuild
	}
	return diagnostic.New(diagnostic.Environment, code, format, args...).WithStage(stage)
}
