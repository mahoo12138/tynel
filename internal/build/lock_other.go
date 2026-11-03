//go:build !unix

package build

import "github.com/Mahoo12138/tynel/compiler/diagnostic"

type outputLock struct{}

func acquireOutputLock(output string) (*outputLock, error) {
	return nil, diagnostic.New(diagnostic.Environment, "build.lock-unsupported", "当前平台没有实现同一输出的构建锁").WithStage(diagnostic.StagePublish)
}

func (l *outputLock) release() {}
