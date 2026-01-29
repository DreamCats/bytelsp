# byte-lsp-mcp

[![Go Version](https://img.shields.io/badge/Go-%3E%3D1.23-blue.svg)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

基于 MCP（Model Context Protocol）的 Go 语言分析服务，通过 gopls 提供**准确的语义分析与代码导航**，可被 Claude Code、Cursor 等支持 MCP 的工具调用。

## 功能特性

只提供 4 个高价值工具，覆盖 Go 代码分析的核心场景：

| 工具 | 功能 | 使用场景 |
|------|------|----------|
| `search_symbols` | 符号搜索 | 探索代码库的入口，找到目标函数/类型 |
| `explain_symbol` | 一站式符号分析 | 理解代码：签名+文档+源码+引用 |
| `explain_import` | 外部依赖类型解析 | 查看 RPC 入参/出参、thrift 生成代码等 |
| `get_call_hierarchy` | 调用链分析 | 追踪代码流：谁调用了它/它调用了谁 |

## 环境要求

- Go 1.23+
- gopls（自动调用，需在 PATH 中）

## 安装

### 从 GitHub 安装

```bash
go install github.com/dreamcats/bytelsp/cmd/byte-lsp-mcp@latest
```

### 公司内网场景

如果公司 GOPROXY 无法拉取 GitHub 模块：

```bash
GOPROXY=direct \
GOPRIVATE=github.com/dreamcats/bytelsp \
GONOSUMDB=github.com/dreamcats/bytelsp \
go install github.com/dreamcats/bytelsp/cmd/byte-lsp-mcp@latest
```

### 从 GitLab 安装

```bash
# 克隆并安装
git clone git@code.byted.org:maifeng/bytelsp.git
cd bytelsp/cmd/byte-lsp-mcp && go install
```

### 从源码构建

```bash
git clone https://github.com/DreamCats/bytelsp.git
cd bytelsp/cmd/byte-lsp-mcp
go build -o byte-lsp-mcp
```

## 配置

### Claude Code / Claude Desktop

在 MCP 配置文件中添加：

```json
{
  "mcpServers": {
    "byte-lsp": {
      "command": "byte-lsp-mcp",
      "args": []
    }
  }
}
```

> 如果 `byte-lsp-mcp` 不在 PATH 中，请使用完整路径。

## 使用说明

### search_symbols - 探索入口

搜索符号是分析代码的第一步。

```json
{
  "name": "search_symbols",
  "arguments": {
    "query": "Handler"
  }
}
```

| 参数 | 必填 | 说明 |
|------|------|------|
| `query` | ✅ | 搜索关键字，支持部分匹配 |
| `include_external` | ❌ | 是否包含标准库/依赖（默认 false） |

### explain_symbol - 理解代码

一次调用获取符号的全部信息。

```json
{
  "name": "explain_symbol",
  "arguments": {
    "file_path": "internal/mcp/server.go",
    "symbol": "Initialize"
  }
}
```

返回：
- 签名和类型信息
- 文档注释
- 源代码
- 定义位置
- 引用列表

| 参数 | 必填 | 说明 |
|------|------|------|
| `file_path` | ✅ | 文件路径 |
| `symbol` | ✅ | 符号名 |
| `include_source` | ❌ | 是否包含源码（默认 true） |
| `include_references` | ❌ | 是否包含引用（默认 true） |
| `max_references` | ❌ | 最大引用数量（默认 10） |

### explain_import - 解析外部依赖

直接从导入包中解析类型/函数定义，无需 gopls 索引。

```json
{
  "name": "explain_import",
  "arguments": {
    "import_path": "github.com/xxx/idl/user",
    "symbol": "GetUserInfoRequest"
  }
}
```

返回：
- 类型定义（完整签名）
- 结构体字段（含 tag 和注释）
- 接口方法
- 文档注释

| 参数 | 必填 | 说明 |
|------|------|------|
| `import_path` | ✅ | Go import 路径 (如 `encoding/json`) |
| `symbol` | ✅ | 类型或函数名 |

**典型场景**：查看 RPC 入参/出参结构、thrift/protobuf 生成的代码。

### get_call_hierarchy - 追踪调用

分析函数的调用关系。

```json
{
  "name": "get_call_hierarchy",
  "arguments": {
    "file_path": "internal/mcp/server.go",
    "symbol": "Initialize",
    "direction": "both"
  }
}
```

返回：
- incoming: 谁调用了这个函数
- outgoing: 这个函数调用了谁

| 参数 | 必填 | 说明 |
|------|------|------|
| `file_path` | ✅ | 文件路径 |
| `symbol` | ✅ | 函数或方法名 |
| `direction` | ❌ | 'incoming'/'outgoing'/'both'（默认） |

## 架构

```
┌─────────────────────────────────────┐
│  Claude Code / Cursor / MCP Client │
└──────────────┬──────────────────────┘
               │ stdio (JSON-RPC)
               ▼
┌─────────────────────────────────────┐
│         byte-lsp-mcp Server         │
│  ┌───────────┐  ┌────────────────┐  │
│  │Tool Handle│  │Document Manager│  │
│  └─────┬─────┘  └───────┬────────┘  │
└────────┼────────────────┼───────────┘
         │ LSP stdio      │
         ▼                ▼
┌─────────────────────────────────────┐
│              gopls                  │
└─────────────────────────────────────┘
```

## 目录结构

```
.
├── cmd/byte-lsp-mcp/     # CLI 入口
└── internal/
    ├── gopls/            # gopls 客户端（LSP 通信）
    ├── mcp/              # MCP 服务器（工具注册与处理）
    ├── tools/            # 类型定义与结果解析
    └── workspace/        # 工作区检测
```

## 命令行参数

```
byte-lsp-mcp [options]

Options:
  -h, -help     显示帮助
  -version      显示版本号
```

## License

[MIT](LICENSE)
