//go:build unix

package build

import (
	"errors"
	"os"
	"syscall"

	"github.com/Mahoo12138/tynel/compiler/diagnostic"
)

// outputLock 是同一输出路径的构建锁。flock 随进程退出自动释放，崩溃不会
// 留下永久锁；锁文件本身保留，避免删除又与新建文件竞争。
type outputLock struct {
	file *os.File
}

func acquireOutputLock(output string) (*outputLock, error) {
	p := newPublication(output)
	f, err := os.OpenFile(p.lock, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, diagnostic.New(diagnostic.Environment, "build.output-unwritable", "无法在 %s 创建构建锁", p.lock).WithDetail(err.Error())
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, diagnostic.New(diagnostic.Environment, "build.output-busy", "另一个构建正在写 %s；等它结束或换一个输出路径", output)
		}
		return nil, diagnostic.New(diagnostic.Environment, "build.lock-failed", "无法锁定输出 %s", output).WithDetail(err.Error())
	}
	return &outputLock{file: f}, nil
}

func (l *outputLock) release() {
	if l == nil || l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
}
