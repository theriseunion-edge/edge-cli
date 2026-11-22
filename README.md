# EdgeCLI

`ec` 命令行工具的实现仓库。用任意 OCI 镜像仓库保存离线包：上传后得到一个发布地址，接收方凭地址下载并校验内容。

设计与范围见 [theriseunion-edge/project](https://github.com/theriseunion-edge/project)：
[设计评审任务 #16](https://github.com/theriseunion-edge/project/issues/16) ·
[命令与架构](https://github.com/theriseunion-edge/project/blob/main/docs/architecture/edge-cli/cli.md)。

## 用法

上传离线包：

```bash
ec package push -i <离线包> -o <发布地址>
```

- 输入支持目录、tar 和 tar.gz；形态原样保存，推目录下载回来是目录，推压缩包下载回来是压缩包。
- 输出发布地址，日志输出形态、大小、内容指纹、分片数、发布地址和 digest。

下载离线包：

```bash
ec package pull -i <发布地址> -o <离线包>
```

- 还原后自动重算内容指纹并校验，不一致立即失败。
- 输出路径已存在时报错，不覆盖。

全局选项：`--format json` 输出结构化结果，`--help`、`--version`；成功返回 0，失败返回非 0。

## 认证

上传使用本机容器凭据（`docker login` 后的仓库可直接使用），不提供登录命令；下载按仓库策略执行，公开仓库匿名可拉。

## 构建与测试

```bash
make build   # 生成 bin/ec
make test    # 单元测试
make vet     # go vet
make e2e     # 用本机 docker registry 跑上传下载端到端测试
```

Go 版本要求：1.24+。

## 内容指纹

按离线包中的文件路径、文件内容 SHA-256 和权限计算，忽略容器形态、时间戳和属主。同一份内容，无论 tar 还是目录，指纹相同。上传时记录指纹，下载时重算比对。

## 仓库职责

本仓库只维护源码、测试、构建配置和开发入口。Issue、架构、计划与参考资料统一在
[theriseunion-edge/project](https://github.com/theriseunion-edge/project)：
[统一 Issue 入口](https://github.com/theriseunion-edge/project/issues) ·
[Project 看板](https://github.com/orgs/theriseunion-edge/projects/2)。

代码 PR 使用完整 project Issue URL 关联任务。
