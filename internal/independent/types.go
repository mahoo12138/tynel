// Package independent 验证生成的 Mach-O 程序能否在隔离环境中运行。
package independent

import "time"

const (
	// SchemaVersion 是独立运行报告的格式版本。
	SchemaVersion = 1
	// Node 是本验收器服务的实施子节点。
	Node = "06.06"
)

// Program 是一个待运行程序及其独立期望输出。
type Program struct {
	Name     string
	Expected string
}

// DefaultPrograms 返回本轮四个端到端示例。期望输出故意不从构建记录或
// clean-build 脚本读取，避免构建与验收共享同一份错误答案。
func DefaultPrograms() []Program {
	return []Program{
		{Name: "add", Expected: "42\n"},
		{Name: "choices", Expected: "-1\n0\n1\n1\nfalse\n3\ntrue\n"},
		{Name: "collatz", Expected: "111\n111\n8\n8\n0\n"},
		{Name: "quote", Expected: "90\n10\n100\n"},
	}
}

// Config 指定产物来源、隔离根和本次证据目录。
type Config struct {
	ProjectRoot  string
	ArtifactsDir string
	OutDir       string
	SandboxPath  string
	NodePath     string
	Timeout      time.Duration
}

// HostInfo 描述实际执行验收的宿主，而不是编译器支持的计划平台。
type HostInfo struct {
	GOOS           string `json:"goos"`
	GOARCH         string `json:"goarch"`
	Uname          string `json:"uname"`
	ProductVersion string `json:"productVersion"`
	BuildVersion   string `json:"buildVersion"`
}

// IsolationInfo 记录进程不能读取的根、可执行文件和最小环境。
type IsolationInfo struct {
	SandboxPath      string   `json:"sandboxPath"`
	ProjectRoot      string   `json:"projectRoot"`
	HomeRoot         string   `json:"homeRoot"`
	PayloadDir       string   `json:"payloadDir"`
	NodePath         string   `json:"nodePath,omitempty"`
	NodeResolvedPath string   `json:"nodeResolvedPath,omitempty"`
	Environment      []string `json:"environment"`
}

// RunResult 保存一次沙箱命令的真实结果。
type RunResult struct {
	ExitCode        int    `json:"exitCode"`
	TimedOut        bool   `json:"timedOut"`
	DurationMillis  int64  `json:"durationMillis"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	OutputTruncated bool   `json:"outputTruncated"`
	StartError      string `json:"startError,omitempty"`
	ProfileSHA256   string `json:"profileSha256"`
	Passed          bool   `json:"passed"`
}

// Artifact 把原始产物、传输副本、构建记录和运行结果对应起来。
type Artifact struct {
	Name              string     `json:"name"`
	SourcePath        string     `json:"sourcePath"`
	PayloadPath       string     `json:"payloadPath"`
	SHA256            string     `json:"sha256"`
	Bytes             int64      `json:"bytes"`
	BuildRecordSHA256 string     `json:"buildRecordSha256"`
	Libraries         []string   `json:"libraries"`
	Run               *RunResult `json:"run,omitempty"`
}

// Probe 验证沙箱本身能启动，并且拒绝规则确实生效。
type Probe struct {
	Name    string     `json:"name"`
	Kind    string     `json:"kind"`
	Target  string     `json:"target,omitempty"`
	Skipped bool       `json:"skipped,omitempty"`
	Detail  string     `json:"detail,omitempty"`
	Result  *RunResult `json:"result,omitempty"`
}

// Report 是本次验收的机器可读证据。
type Report struct {
	SchemaVersion int           `json:"schemaVersion"`
	Node          string        `json:"node"`
	GeneratedAt   string        `json:"generatedAt"`
	Status        string        `json:"status"`
	Failure       string        `json:"failure,omitempty"`
	Host          HostInfo      `json:"host"`
	Isolation     IsolationInfo `json:"isolation"`
	Artifacts     []Artifact    `json:"artifacts"`
	Probes        []Probe       `json:"probes"`
}

// BlockedError 表示隔离环境本身不可用；它与程序输出不一致不同。
type BlockedError struct {
	Reason string
}

func (e *BlockedError) Error() string { return e.Reason }
