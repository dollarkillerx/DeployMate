# DeployMate

DeployMate 是面向 AI 部署工作的远程 MCP Agent。每台 Linux 服务器独立运行一个
`deploymate-agent`，通过 HTTPS 暴露 MCP Streamable HTTP 接口，并由 systemd 管理。

> **高风险能力：** 默认 systemd 服务以 root 运行，MCP 可以执行任意 shell 命令并读写
> 整个文件系统。Bearer token 等同于远程 root 凭据。只应在受控环境中使用，并限制公网
> 9443 端口的来源地址。

## 功能

- 获取主机、OS、CPU、内存、磁盘、网络、uptime 和 systemd 信息。
- 同步执行命令，支持超时、输出上限和并发限制。
- 异步执行命令，支持任务查询、增量日志、取消和重启恢复标记。
- 通过短时、一次性 HTTP URL 上传和下载不超过 100 MiB 的文件。
- 首次启动自动生成 ECDSA P-256 自签证书和 256-bit Bearer token。
- 每台服务器独立注册为一个 MCP，例如 `deploymate-prod-web-1`。

## 构建

需要 Go 1.24 或更高版本。

```bash
go test ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags "-s -w -X main.version=$(git describe --always --dirty)" \
  -o deploymate-agent ./cmd/deploymate-agent
```

ARM64 服务器将 `GOARCH` 改为 `arm64`。

## 安装

将仓库中的以下内容按原目录结构复制到远程服务器：

```text
deploymate-agent
scripts/install-agent.sh
packaging/systemd/deploymate-agent.service
```

然后执行：

```bash
sudo ./scripts/install-agent.sh install
```

可通过环境变量指定公网证书名称和监听地址：

```bash
sudo DEPLOYMATE_PUBLIC_HOST=203.0.113.10 \
  DEPLOYMATE_LISTEN=0.0.0.0:9443 \
  ./scripts/install-agent.sh install
```

安装脚本会输出可复制到本机 `servers.yaml` 的配置。其他操作：

```bash
sudo ./scripts/install-agent.sh upgrade
sudo ./scripts/install-agent.sh rotate-token
sudo ./scripts/install-agent.sh uninstall
```

升级时通过 `DEPLOYMATE_BINARY=/path/to/new/deploymate-agent` 指定新二进制。卸载默认保留
`/etc/deploymate` 和 `/var/lib/deploymate`，避免误删凭据与任务记录。

## 配置

远端配置位于 `/etc/deploymate/agent.yaml`，完整示例见
[`examples/agent.yaml`](examples/agent.yaml)。关键默认值：

```yaml
listen: 0.0.0.0:9443
tls:
  certificate_file: /etc/deploymate/tls/server.crt
  private_key_file: /etc/deploymate/tls/server.key
  auto_generate: true
  hosts: [203.0.113.10]
auth:
  token_hash_file: /etc/deploymate/token.sha256
  initial_token_file: /etc/deploymate/initial-token
limits:
  max_concurrent_commands: 4
  max_file_size: 104857600
  max_timeout_seconds: 86400
  transfer_ticket_ttl_seconds: 600
  http_read_timeout_seconds: 1800
```

首次启动生成的明文 token 位于 `/etc/deploymate/initial-token`，权限为 `0600`。Agent
只使用 `/etc/deploymate/token.sha256` 验证请求。轮换 token 后必须更新所有 MCP 客户端。
配置使用严格 YAML 字段校验；未知字段、非正数超时和非法限制会导致 Agent 拒绝启动。

## 本机服务器清单

[`examples/servers.yaml`](examples/servers.yaml) 是 DeployMate 的统一清单示例，不需要运行
本地 DeployMate 服务：

```yaml
servers:
  prod-web-1:
    url: https://203.0.113.10:9443/mcp
    token: dm_replace_me
    insecure_skip_verify: true
```

该 YAML 是清单，不是通用 MCP 客户端标准。需要把每项映射到实际 AI 客户端的配置中。
清单包含 root token，务必设置为 `0600`，不要提交到 Git。

### Codex

Codex 可使用环境变量提供 Bearer token：

```bash
export DEPLOYMATE_PROD_WEB_1_TOKEN='dm_replace_me'
codex mcp add deploymate-prod-web-1 \
  --url https://203.0.113.10:9443/mcp \
  --bearer-token-env-var DEPLOYMATE_PROD_WEB_1_TOKEN
```

当前 Codex CLI 的远程 MCP 参数不提供跳过证书校验的选项。使用自签证书时，需要把
`/etc/deploymate/tls/server.crt` 导入本机系统信任库，或在 Agent 前配置受信任的公网证书。

### Claude Code

```bash
claude mcp add --transport http --scope user \
  --header "Authorization: Bearer dm_replace_me" \
  deploymate-prod-web-1 https://203.0.113.10:9443/mcp
```

是否可以跳过自签证书校验取决于客户端版本和运行时。若客户端没有对应选项，同样需要
导入自签证书或使用受信任证书。

### TLS 风险

`insecure_skip_verify: true` 表示连接仍加密，但客户端不验证服务器身份。公网攻击者可能
通过中间人攻击窃取 Bearer token。支持该选项的客户端可以按需求启用，但生产环境推荐
信任 Agent 自签证书或配置正式证书。

## MCP 工具

| 工具 | 用途 |
|---|---|
| `get_server_info` | 获取服务器基础信息 |
| `exec_command` | 同步执行 `/bin/sh -lc` 命令 |
| `start_command` | 创建异步命令任务 |
| `get_job` | 查询任务和游标后的增量日志 |
| `cancel_job` | 取消任务及其进程组 |
| `list_jobs` | 按状态列出最近任务 |
| `create_upload` | 创建一次性 HTTP PUT 上传 URL |
| `create_download` | 创建一次性 HTTP GET 下载 URL |

上传示例：

```bash
curl -k --fail --upload-file ./app.tar.gz 'https://server:9443/files/upload/up_xxx'
```

下载示例：

```bash
curl -k --fail --output ./app.tar.gz 'https://server:9443/files/download/down_xxx'
```

文件 URL 默认 10 分钟过期且只能使用一次。上传票据绑定远端路径、大小和 SHA-256；
Agent 校验成功后才会把临时文件原子移动到目标路径。
HTTP 请求体默认最多允许传输 30 分钟。命令 stdout 和 stderr 各自最多保留 4 MiB，工具参数
只能进一步降低该上限，不能提高服务端硬限制。

## 运维

```bash
systemctl status deploymate-agent
journalctl -u deploymate-agent -f
```

审计日志包含请求 ID、客户端 IP、操作类型、HTTP 状态、响应字节数和耗时。日志不会记录
Authorization header、Bearer token、文件票据、文件内容或环境变量值。

公网部署至少应配置：

- 防火墙仅允许可信来源访问 TCP 9443。
- 每台服务器使用不同 token。
- 定期轮换 token。
- 将 `/etc/deploymate` 设为仅 root 可读。
- 优先启用证书验证，不长期依赖 `insecure_skip_verify`。

## 开发验证

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/deploymate-agent
sh -n scripts/install-agent.sh
```

项目使用 GPL-3.0 许可证。
