package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/theriseunion-edge/edge-cli/internal/cli"
)

func run(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := cli.Run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestVersion(t *testing.T) {
	code, stdout, _ := run("--version")
	if code != 0 || !strings.HasPrefix(stdout, "ec ") {
		t.Fatalf("版本输出错误: code=%d stdout=%q", code, stdout)
	}
}

func TestHelp(t *testing.T) {
	code, stdout, _ := run("--help")
	if code != 0 || !strings.Contains(stdout, "ec package push") {
		t.Fatalf("帮助输出错误: code=%d stdout=%q", code, stdout)
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, stderr := run("unknown")
	if code != 2 || !strings.Contains(stderr, "未知命令") {
		t.Fatalf("未知命令处理错误: code=%d stderr=%q", code, stderr)
	}
}

func TestMissingFlags(t *testing.T) {
	for _, args := range [][]string{
		{"package", "push"},
		{"package", "push", "-i", "pkg.tgz"},
		{"package", "pull", "-o", "out"},
	} {
		code, _, stderr := run(args...)
		if code != 2 || !strings.Contains(stderr, "必填") {
			t.Fatalf("%v 参数校验错误: code=%d stderr=%q", args, code, stderr)
		}
	}
}

func TestBadFormat(t *testing.T) {
	code, _, stderr := run("--format", "yaml", "package", "push", "-i", "a", "-o", "b")
	if code != 2 || !strings.Contains(stderr, "不支持的输出格式") {
		t.Fatalf("格式校验错误: code=%d stderr=%q", code, stderr)
	}
}
