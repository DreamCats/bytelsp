# byte-lsp-mcp

[![Go Version](https://img.shields.io/badge/Go-%3E%3D1.23-blue.svg)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

基于 MCP（Model Context Protocol）的 Go 语言分析服务，通过 gopls 提供**准确的语义分析、诊断与代码导航**，可被 Claude Code、Cursor 等支持 MCP 的工具调用。

## 功能特性

| 工具 | 功能 | 说明 |
|------|------|------|
| `explain_symbol` | 一站式符号分析 | 签名+文档+源码+引用，推荐使用 |
| `get_call_hierarchy` | 调用链分析 | 查看谁调用了函数/函数调用了谁 |
| `analyze_code` | 代码诊断 | 错误/警告/提示 |
| `go_to_definition` | 跳转定义 | 支持行列号或符号名 |
| `find_references` | 查找引用 | 支持行列号或符号名 |
| `search_symbols` | 符号搜索 | 默认仅工作区 |
| `get_hover` | 悬浮信息 | 类型/签名/注释 |

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

### explain_symbol（推荐）

一站式获取符号的完整信息，包括签名、文档、源码和引用。**比分别调用多个工具更高效**。

```json
{
  "name": "explain_symbol",
  "arguments": {
    "file_path": "internal/mcp/server.go",
    "symbol": "Initialize"
  }
}
```

返回示例：

```json
{
  "name": "Initialize",
  "kind": "Method",
  "signature": "func (s *Service) Initialize(ctx context.Context) error",
  "doc": "Initialize starts gopls client and registers diagnostics listener.",
  "source": "func (s *Service) Initialize(ctx context.Context) error { ... }",
  "defined_at": { "file_path": "server.go", "line": 139, "col": 1 },
  "references_count": 5,
  "references": [
    { "file_path": "server.go", "line": 168, "context": "if err := s.Initialize(ctx); err != nil {" }
  ]
}
```

| 参数 | 必填 | 说明 |
|------|------|------|
| `file_path` | ✅ | 文件路径 |
| `symbol` | ✅ | 符号名（函数/类型/变量） |
| `include_source` | ❌ | 是否包含源码（默认 true） |
| `include_references` | ❌ | 是否包含引用（默认 true） |
| `max_references` | ❌ | 最大引用数量（默认 10） |

### get_call_hierarchy

分析函数/方法的调用关系。

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

返回示例：

```json
{
  "name": "Initialize",
  "kind": "Method",
  "file_path": "internal/mcp/server.go",
  "line": 139,
  "incoming": [
    { "name": "AnalyzeCode", "kind": "Method", "file_path": "server.go", "line": 168, "context": "if err := s.Initialize(ctx); err != nil {" }
  ],
  "outgoing": [
    { "name": "NewClient", "kind": "Function", "file_path": "client.go", "line": 25 },
    { "name": "NewDocumentManager", "kind": "Function", "file_path": "documents.go", "line": 10 }
  ]
}
```

| 参数 | 必填 | 说明 |
|------|------|------|
| `file_path` | ✅ | 文件路径 |
| `symbol` | ✅ | 函数或方法名 |
| `direction` | ❌ | 方向：'incoming'(调用者)/'outgoing'(被调用)/'both'(默认) |
| `depth` | ❌ | 遍历深度（默认 1，暂只支持 1 层） |

### analyze_code

分析代码并返回诊断信息。

```json
{
  "name": "analyze_code",
  "arguments": {
    "code": "package main\n\nfunc main() {\n\tx := 1\n}",
    "file_path": "main.go",
    "include_warnings": true
  }
}
```

| 参数 | 必填 | 说明 |
|------|------|------|
| `code` | ✅ | Go 代码内容 |
| `file_path` | ✅ | 文件路径 |
| `include_warnings` | ❌ | 是否包含 warning/info/hint（默认 false） |

### go_to_definition / find_references / get_hover

文件内容**自动从磁盘读取**，支持两种定位方式：

**方式一：符号名（推荐）**

```json
{
  "name": "go_to_definition",
  "arguments": {
    "file_path": "internal/mcp/server.go",
    "symbol": "Initialize"
  }
}
```

**方式二：行列号**

```json
{
  "name": "go_to_definition",
  "arguments": {
    "file_path": "internal/mcp/server.go",
    "line": 139,
    "col": 20
  }
}
```

| 参数 | 必填 | 说明 |
|------|------|------|
| `file_path` | ✅ | 文件路径 |
| `symbol` | ❌ | 符号名（推荐，与 line/col 二选一） |
| `line` | ❌ | 行号（1-based） |
| `col` | ❌ | 列号（1-based） |
| `code` | ❌ | 仅当文件不存在时需要（如未保存的缓冲区） |

### search_symbols

搜索工作区符号。

```json
{
  "name": "search_symbols",
  "arguments": {
    "query": "Handler",
    "include_external": false
  }
}
```

| 参数 | 必填 | 说明 |
|------|------|------|
| `query` | ✅ | 搜索关键字 |
| `include_external` | ❌ | 是否包含标准库/依赖（默认 false） |

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
