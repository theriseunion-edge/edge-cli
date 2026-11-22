// Package cli 解析命令参数并执行上传和下载。
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/theriseunion-edge/edge-cli/internal/ocistore"
	"github.com/theriseunion-edge/edge-cli/internal/ops"
	"github.com/theriseunion-edge/edge-cli/internal/pack"
)

// Version 由构建参数注入。
var Version = "dev"

const usageText = `EdgeCLI 用 OCI 镜像仓库保存离线包。

用法:
  ec package push -i <离线包> -o <发布地址>
  ec package pull -i <发布地址> -o <离线包>

参数:
  -i, --input    输入：push 为本地离线包（目录、tar 或 tar.gz），pull 为发布地址
  -o, --output   输出：push 为发布地址，pull 为本地离线包路径
      --format   输出格式：text（默认）或 json
  -h, --help     显示帮助
  -v, --version  显示版本
`

// Run 执行命令并返回退出码。
func Run(args []string, stdout, stderr io.Writer) int {
	format, rest, err := parseGlobal(args)
	if err != nil {
		return usageError(stderr, err.Error())
	}
	if len(rest) == 0 {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	switch rest[0] {
	case "--help", "-h", "help":
		fmt.Fprint(stdout, usageText)
		return 0
	case "--version", "-v", "version":
		fmt.Fprintln(stdout, "ec "+Version)
		return 0
	case "package":
		return runPackage(rest[1:], format, stdout, stderr)
	default:
		return usageError(stderr, "未知命令: "+rest[0])
	}
}

func parseGlobal(args []string) (string, []string, error) {
	format := "text"
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--format":
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("--format 缺少取值")
			}
			format = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--format="):
			format = strings.TrimPrefix(args[i], "--format=")
		default:
			rest = append(rest, args[i])
		}
	}
	if format != "text" && format != "json" {
		return "", nil, fmt.Errorf("不支持的输出格式: %s", format)
	}
	return format, rest, nil
}

func runPackage(args []string, format string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	switch args[0] {
	case "--help", "-h", "help":
		fmt.Fprint(stdout, usageText)
		return 0
	case "push":
		return runPush(args[1:], format, stdout, stderr)
	case "pull":
		return runPull(args[1:], format, stdout, stderr)
	default:
		return usageError(stderr, "未知命令: package "+args[0])
	}
}

type commandArgs struct {
	input  string
	output string
}

func parseCommandArgs(name string, args []string) (commandArgs, error) {
	fs := flag.NewFlagSet("ec package "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var input, output string
	fs.StringVar(&input, "i", "", "输入")
	fs.StringVar(&input, "input", "", "输入")
	fs.StringVar(&output, "o", "", "输出")
	fs.StringVar(&output, "output", "", "输出")
	if err := fs.Parse(args); err != nil {
		return commandArgs{}, err
	}
	if fs.NArg() > 0 {
		return commandArgs{}, fmt.Errorf("多余的参数: %s", strings.Join(fs.Args(), " "))
	}
	if input == "" {
		return commandArgs{}, fmt.Errorf("-i/--input 必填")
	}
	if output == "" {
		return commandArgs{}, fmt.Errorf("-o/--output 必填")
	}
	return commandArgs{input: input, output: output}, nil
}

func runPush(args []string, format string, stdout, stderr io.Writer) int {
	parsed, err := parseCommandArgs("push", args)
	if err != nil {
		return usageError(stderr, err.Error())
	}
	st, err := ocistore.New()
	if err != nil {
		return fail(stderr, err)
	}
	result, err := ops.Push(context.Background(), st, parsed.input, parsed.output, pack.DefaultChunkSize, logTo(stderr))
	if err != nil {
		return fail(stderr, err)
	}
	printResult(stdout, format, result)
	return 0
}

func runPull(args []string, format string, stdout, stderr io.Writer) int {
	parsed, err := parseCommandArgs("pull", args)
	if err != nil {
		return usageError(stderr, err.Error())
	}
	st, err := ocistore.New()
	if err != nil {
		return fail(stderr, err)
	}
	result, err := ops.Pull(context.Background(), st, parsed.input, parsed.output, pack.DefaultChunkSize, logTo(stderr))
	if err != nil {
		return fail(stderr, err)
	}
	printResult(stdout, format, result)
	return 0
}

func logTo(w io.Writer) ops.LogFunc {
	return func(message string, args ...any) {
		fmt.Fprintf(w, message+"\n", args...)
	}
}

func printResult(w io.Writer, format string, result *ops.Result) {
	if format == "json" {
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			fmt.Fprintf(w, "输出结果失败: %v\n", err)
			return
		}
		fmt.Fprintln(w, string(data))
		return
	}
	if result.Command == "push" {
		fmt.Fprintf(w, "形态: %s\n", result.Form)
		fmt.Fprintf(w, "大小: %d\n", result.Size)
		fmt.Fprintf(w, "内容指纹: %s\n", result.Fingerprint)
		fmt.Fprintf(w, "分片: %d\n", result.Chunks)
		fmt.Fprintf(w, "发布地址: %s\n", result.Reference)
		return
	}
	fmt.Fprintf(w, "形态: %s\n", result.Form)
	fmt.Fprintf(w, "输出: %s\n", result.Output)
	fmt.Fprintf(w, "内容指纹: %s\n", result.Fingerprint)
	fmt.Fprintf(w, "分片: %d\n", result.Chunks)
	if result.Verified {
		fmt.Fprintln(w, "校验: 通过")
	}
}

func usageError(w io.Writer, message string) int {
	fmt.Fprintln(w, "错误: "+message)
	fmt.Fprint(w, usageText)
	return 2
}

func fail(w io.Writer, err error) int {
	fmt.Fprintln(w, "错误: "+err.Error())
	return 1
}
